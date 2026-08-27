package trace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/observability/privacy"
	"github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	ExportFormat        = "codehelper.observation-jsonl"
	ExportFormatVersion = 1
	ExportMediaType     = "application/x-ndjson"
)

type ObservationSnapshotter interface {
	Snapshot(context.Context) ([]observation.Envelope, error)
}

type ExportScopeReader interface {
	SessionReader
	ThreadIDs(context.Context, string) ([]protocol.ThreadID, error)
	TurnIDs(context.Context, string) ([]protocol.TurnID, error)
}

type ExportUsageReader interface {
	QueryAggregates(context.Context, usage.Query) ([]usage.Aggregate, error)
}

type ExportRequest struct {
	SessionID       string
	ProducerVersion string
}

type ExportResult struct {
	Filename  string
	MediaType string
	Content   []byte
	Manifest  ExportManifest
}

type ExportManifest struct {
	RecordType               string    `json:"record_type"`
	Format                   string    `json:"format"`
	FormatVersion            int       `json:"format_version"`
	ObservationSchemaVersion uint32    `json:"observation_schema_version"`
	Producer                 string    `json:"producer"`
	ProducerVersion          string    `json:"producer_version"`
	SessionID                string    `json:"session_id"`
	ThroughSequence          uint64    `json:"through_sequence"`
	EventCount               uint64    `json:"event_count"`
	UsageCount               uint64    `json:"usage_count"`
	PayloadMode              string    `json:"payload_mode"`
	SummaryMode              string    `json:"summary_mode"`
	RecordsSHA256            string    `json:"records_sha256"`
	ExportedAt               time.Time `json:"exported_at"`
}

type exportObservation struct {
	RecordType     string               `json:"record_type"`
	Envelope       observation.Envelope `json:"envelope"`
	PayloadOmitted bool                 `json:"payload_omitted,omitempty"`
	SummaryOmitted bool                 `json:"summary_omitted,omitempty"`
}

type exportUsage struct {
	RecordType      string            `json:"record_type"`
	SessionID       string            `json:"session_id"`
	ThreadID        protocol.ThreadID `json:"thread_id,omitempty"`
	TurnID          protocol.TurnID   `json:"turn_id,omitempty"`
	Provider        string            `json:"provider"`
	Model           string            `json:"model"`
	InputTokens     uint64            `json:"input_tokens"`
	OutputTokens    uint64            `json:"output_tokens"`
	ReasoningTokens uint64            `json:"reasoning_tokens"`
	CachedTokens    uint64            `json:"cached_tokens"`
	CostMicrounits  uint64            `json:"cost_microunits"`
	PricedCalls     uint64            `json:"priced_calls"`
	UnpricedCalls   uint64            `json:"unpriced_calls"`
	Calls           uint64            `json:"calls"`
	FirstAt         time.Time         `json:"first_at"`
	LastAt          time.Time         `json:"last_at"`
}

type ExportService struct {
	sessions      ExportScopeReader
	observations  ObservationSnapshotter
	usage         ExportUsageReader
	workspaceRoot string
	now           func() time.Time
}

func NewExportService(
	sessions ExportScopeReader,
	observations ObservationSnapshotter,
	usageReader ExportUsageReader,
	workspaceRoot string,
) *ExportService {
	return &ExportService{
		sessions: sessions, observations: observations,
		usage: usageReader, workspaceRoot: canonicalWorkspaceRoot(workspaceRoot),
		now: time.Now,
	}
}

func (s *ExportService) Export(
	ctx context.Context,
	request ExportRequest,
) (ExportResult, error) {
	if s == nil || s.sessions == nil || s.observations == nil {
		return ExportResult{}, queryProblem(
			protocol.CodeUnavailable,
			"trace export is unavailable",
			nil,
		)
	}
	request.SessionID = strings.TrimSpace(request.SessionID)
	if request.SessionID == "" {
		return ExportResult{}, queryProblem(
			protocol.CodeInvalidArgument,
			"trace export requires a session",
			nil,
		)
	}
	session, err := s.sessions.GetLifecycle(ctx, request.SessionID)
	if err != nil {
		return ExportResult{}, err
	}
	if s.workspaceRoot != "." &&
		canonicalWorkspaceRoot(session.WorkspaceRoot) != s.workspaceRoot {
		return ExportResult{}, queryProblem(
			protocol.CodeInvalidArgument,
			"trace export session does not belong to this Workspace",
			nil,
		)
	}
	threadIDs, err := s.sessions.ThreadIDs(ctx, request.SessionID)
	if err != nil {
		return ExportResult{}, fmt.Errorf("resolve trace export threads: %w", err)
	}
	turnIDs, err := s.sessions.TurnIDs(ctx, request.SessionID)
	if err != nil {
		return ExportResult{}, fmt.Errorf("resolve trace export turns: %w", err)
	}
	threadSet := make(map[protocol.ThreadID]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		threadSet[threadID] = struct{}{}
	}
	turnSet := make(map[protocol.TurnID]struct{}, len(turnIDs))
	for _, turnID := range turnIDs {
		turnSet[turnID] = struct{}{}
	}
	envelopes, err := s.observations.Snapshot(ctx)
	if err != nil {
		return ExportResult{}, fmt.Errorf("snapshot observations: %w", err)
	}
	var restrictedPaths []string
	if s.workspaceRoot != "." {
		restrictedPaths = append(restrictedPaths, s.workspaceRoot)
	}
	if strings.TrimSpace(session.WorkspaceRoot) != "" {
		restrictedPaths = append(restrictedPaths, session.WorkspaceRoot)
	}
	exportPolicy, err := privacy.New(privacy.Options{
		Mode: privacy.CaptureMetadata, RestrictedPaths: restrictedPaths,
	})
	if err != nil {
		return ExportResult{}, fmt.Errorf("create trace export privacy policy: %w", err)
	}

	var recordLines bytes.Buffer
	var count, through uint64
	for _, envelope := range envelopes {
		if envelope.Sequence > through {
			through = envelope.Sequence
		}
		_, ownedThread := threadSet[envelope.Identity.ThreadID]
		_, ownedTurn := turnSet[envelope.Identity.TurnID]
		if envelope.Identity.SessionID != request.SessionID &&
			!ownedThread && !ownedTurn {
			continue
		}
		record, err := externalObservation(envelope, exportPolicy)
		if err != nil {
			return ExportResult{}, fmt.Errorf("redact observation summary: %w", err)
		}
		line, err := json.Marshal(record)
		if err != nil {
			return ExportResult{}, fmt.Errorf("encode observation: %w", err)
		}
		recordLines.Write(line)
		recordLines.WriteByte('\n')
		count++
	}
	var usageCount uint64
	if s.usage != nil {
		aggregates, err := s.usage.QueryAggregates(ctx, usage.Query{
			SessionID: request.SessionID, IncludeChildren: true,
		})
		if err != nil {
			return ExportResult{}, fmt.Errorf("query trace export usage: %w", err)
		}
		for _, aggregate := range aggregates {
			line, err := json.Marshal(exportUsageFrom(aggregate))
			if err != nil {
				return ExportResult{}, fmt.Errorf("encode trace usage: %w", err)
			}
			recordLines.Write(line)
			recordLines.WriteByte('\n')
			usageCount++
		}
	}
	recordsDigest := sha256.Sum256(recordLines.Bytes())
	producerVersion := strings.TrimSpace(request.ProducerVersion)
	if producerVersion == "" {
		producerVersion = "unknown"
	}
	manifest := ExportManifest{
		RecordType:               "manifest",
		Format:                   ExportFormat,
		FormatVersion:            ExportFormatVersion,
		ObservationSchemaVersion: observation.SchemaVersion,
		Producer:                 "codehelper",
		ProducerVersion:          producerVersion,
		SessionID:                request.SessionID,
		ThroughSequence:          through,
		EventCount:               count,
		UsageCount:               usageCount,
		PayloadMode:              "omitted",
		SummaryMode:              "safe_metadata",
		RecordsSHA256:            hex.EncodeToString(recordsDigest[:]),
		ExportedAt:               s.now().UTC(),
	}
	manifestLine, err := json.Marshal(manifest)
	if err != nil {
		return ExportResult{}, fmt.Errorf("encode trace export manifest: %w", err)
	}
	content := make([]byte, 0, len(manifestLine)+1+recordLines.Len())
	content = append(content, manifestLine...)
	content = append(content, '\n')
	content = append(content, recordLines.Bytes()...)
	filenameDigest := sha256.Sum256([]byte(request.SessionID))
	return ExportResult{
		Filename: fmt.Sprintf(
			"codehelper-trace-%s.ndjson",
			hex.EncodeToString(filenameDigest[:6]),
		),
		MediaType: ExportMediaType,
		Content:   content,
		Manifest:  manifest,
	}, nil
}

func canonicalWorkspaceRoot(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "."
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(absolute)
}

func exportUsageFrom(value usage.Aggregate) exportUsage {
	return exportUsage{
		RecordType: "usage", SessionID: value.SessionID,
		ThreadID: value.ThreadID, TurnID: value.TurnID,
		Provider: value.Provider, Model: value.Model,
		InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
		ReasoningTokens: value.ReasoningTokens, CachedTokens: value.CachedTokens,
		CostMicrounits: value.CostMicrounits,
		PricedCalls:    value.PricedCalls, UnpricedCalls: value.UnpricedCalls,
		Calls: value.Calls, FirstAt: value.FirstAt, LastAt: value.LastAt,
	}
}

func externalObservation(
	envelope observation.Envelope,
	exportPolicy *privacy.Policy,
) (exportObservation, error) {
	cloned := envelope
	cloned.Summary = append(json.RawMessage(nil), envelope.Summary...)
	result := exportObservation{
		RecordType:     "observation",
		Envelope:       cloned,
		PayloadOmitted: cloned.Payload != nil,
	}
	cloned.Payload = nil
	if cloned.Policy.Class == observation.DataWorkspace ||
		cloned.Policy.Class == observation.DataConversation ||
		cloned.Policy.Class == observation.DataCredential ||
		cloned.Policy.Class == observation.DataRestricted ||
		cloned.Policy.Redaction == observation.RedactionUnavailable {
		result.SummaryOmitted = len(cloned.Summary) != 0
		cloned.Summary = nil
	} else if len(cloned.Summary) != 0 {
		summary, err := exportPolicy.RedactBytes(
			cloned.Summary,
			"application/json",
		)
		if err != nil {
			return exportObservation{}, err
		}
		cloned.Summary = summary
	}
	result.Envelope = cloned
	return result, nil
}
