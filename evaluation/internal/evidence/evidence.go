package evidence

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const SchemaVersion = 1

type Source string

const (
	SourceHost        Source = "host"
	SourceACP         Source = "acp"
	SourceRuntime     Source = "runtime"
	SourceProvider    Source = "provider"
	SourceObservation Source = "observation"
	SourceHarness     Source = "harness"
)

type DataClass string

const (
	DataPublicMetadata DataClass = "public_metadata"
	DataOperational    DataClass = "operational"
	DataWorkspace      DataClass = "workspace_content"
	DataConversation   DataClass = "conversation_content"
	DataCredential     DataClass = "credential"
	DataRestricted     DataClass = "restricted"
)

type Redaction string

const (
	RedactionNotRequired Redaction = "not_required"
	RedactionApplied     Redaction = "applied"
	RedactionDropped     Redaction = "dropped"
)

type Identity struct {
	Capture   string `json:"capture,omitempty"`
	Session   string `json:"session,omitempty"`
	Thread    string `json:"thread,omitempty"`
	Turn      string `json:"turn,omitempty"`
	Operation string `json:"operation,omitempty"`
	Item      string `json:"item,omitempty"`
	Event     string `json:"event,omitempty"`
	Effect    string `json:"effect,omitempty"`
	Attempt   string `json:"attempt,omitempty"`
	Request   string `json:"request,omitempty"`
	Run       string `json:"run,omitempty"`
	Node      string `json:"node,omitempty"`
	Resume    string `json:"resume,omitempty"`
	Call      string `json:"call,omitempty"`
	Sample    string `json:"sample,omitempty"`
}

type Causality struct {
	ParentSequence uint64   `json:"parent_sequence,omitempty"`
	Links          []uint64 `json:"links,omitempty"`
}

type Policy struct {
	Class     DataClass `json:"class"`
	Redaction Redaction `json:"redaction"`
}

type Envelope struct {
	SchemaVersion  int             `json:"schema_version"`
	Sequence       uint64          `json:"sequence"`
	OffsetMS       int64           `json:"offset_ms"`
	Source         Source          `json:"source"`
	Kind           string          `json:"kind"`
	Identity       Identity        `json:"identity"`
	Causality      Causality       `json:"causality"`
	Policy         Policy          `json:"policy"`
	Data           json.RawMessage `json:"data"`
	PreviousDigest string          `json:"previous_digest,omitempty"`
	Digest         string          `json:"digest"`
}

type RawEnvelope struct {
	ObservedSequence uint64
	ObservedAt       string
	Source           Source
	Kind             string
	Identity         Identity
	Data             any
	Redacted         bool
}

var (
	idPattern     = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[0-9]{3})$`)
	kindPattern   = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func (e Envelope) Validate(previous string) error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("evidence schema_version = %d, want %d", e.SchemaVersion, SchemaVersion)
	}
	if e.Sequence == 0 || e.OffsetMS < 0 || !validSource(e.Source) ||
		!kindPattern.MatchString(e.Kind) {
		return errors.New("evidence sequence, offset, source, or kind is invalid")
	}
	if err := e.Identity.Validate(); err != nil {
		return err
	}
	if err := e.Causality.Validate(e.Sequence); err != nil {
		return err
	}
	if !validClass(e.Policy.Class) || !validRedaction(e.Policy.Redaction) {
		return errors.New("evidence policy is invalid")
	}
	if e.Policy.Class == DataCredential || e.Policy.Class == DataRestricted {
		if e.Policy.Redaction != RedactionDropped {
			return errors.New("credential and restricted evidence must be dropped")
		}
	}
	if len(e.Data) == 0 || !json.Valid(e.Data) {
		return errors.New("evidence data must be valid JSON")
	}
	if e.PreviousDigest != previous {
		return fmt.Errorf("evidence previous_digest = %q, want %q", e.PreviousDigest, previous)
	}
	if !digestPattern.MatchString(e.Digest) {
		return errors.New("evidence digest is invalid")
	}
	expected, err := e.CalculateDigest()
	if err != nil {
		return err
	}
	if e.Digest != expected {
		return errors.New("evidence digest does not match content")
	}
	return nil
}

func (i Identity) Validate() error {
	values := []string{
		i.Capture, i.Session, i.Thread, i.Turn, i.Operation, i.Item,
		i.Event, i.Effect, i.Attempt, i.Request, i.Run, i.Node,
		i.Resume, i.Call, i.Sample,
	}
	for _, value := range values {
		if value != "" && !idPattern.MatchString(value) {
			return fmt.Errorf("evidence canonical identity %q is invalid", value)
		}
	}
	return nil
}

func (c Causality) Validate(sequence uint64) error {
	if c.ParentSequence >= sequence {
		return errors.New("evidence parent_sequence must precede the event")
	}
	seen := make(map[uint64]struct{}, len(c.Links))
	for _, link := range c.Links {
		if link == 0 || link >= sequence {
			return errors.New("evidence causal link must precede the event")
		}
		if _, exists := seen[link]; exists {
			return errors.New("evidence causal links contain a duplicate")
		}
		seen[link] = struct{}{}
	}
	return nil
}

func (e Envelope) CalculateDigest() (string, error) {
	copy := e
	copy.Digest = ""
	encoded, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func Seal(events []Envelope) ([]Envelope, error) {
	sealed := append([]Envelope(nil), events...)
	previous := ""
	for index := range sealed {
		sealed[index].SchemaVersion = SchemaVersion
		sealed[index].Sequence = uint64(index + 1)
		sealed[index].PreviousDigest = previous
		digest, err := sealed[index].CalculateDigest()
		if err != nil {
			return nil, err
		}
		sealed[index].Digest = digest
		previous = digest
	}
	if err := ValidateAll(sealed); err != nil {
		return nil, err
	}
	return sealed, nil
}

func ValidateAll(events []Envelope) error {
	if len(events) == 0 {
		return errors.New("evidence trace is empty")
	}
	previous := ""
	var previousOffset int64
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			return fmt.Errorf("evidence sequence %d is not contiguous", event.Sequence)
		}
		if index > 0 && event.OffsetMS < previousOffset {
			return errors.New("evidence offset is out of order")
		}
		if err := event.Validate(previous); err != nil {
			return fmt.Errorf("evidence sequence %d: %w", event.Sequence, err)
		}
		previous = event.Digest
		previousOffset = event.OffsetMS
	}
	return nil
}

func ReadJSONL(path string) ([]Envelope, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return DecodeJSONL(file)
}

func DecodeJSONL(reader io.Reader) ([]Envelope, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	var events []Envelope
	line := 0
	for scanner.Scan() {
		line++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var event Envelope
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			return nil, fmt.Errorf("decode evidence line %d: %w", line, err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("evidence line %d contains multiple values", line)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := ValidateAll(events); err != nil {
		return nil, err
	}
	return events, nil
}

func EncodeJSONL(events []Envelope) ([]byte, error) {
	if err := ValidateAll(events); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return nil, err
		}
	}
	return output.Bytes(), nil
}

func DigestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validSource(source Source) bool {
	switch source {
	case SourceHost, SourceACP, SourceRuntime, SourceProvider,
		SourceObservation, SourceHarness:
		return true
	default:
		return false
	}
}

func validClass(class DataClass) bool {
	switch class {
	case DataPublicMetadata, DataOperational, DataWorkspace,
		DataConversation, DataCredential, DataRestricted:
		return true
	default:
		return false
	}
}

func validRedaction(redaction Redaction) bool {
	switch redaction {
	case RedactionNotRequired, RedactionApplied, RedactionDropped:
		return true
	default:
		return false
	}
}

func NormalizeKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.ReplaceAll(kind, "/", ".")
	return strings.ReplaceAll(kind, " ", "_")
}
