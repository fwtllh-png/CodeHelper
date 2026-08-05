package promptcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

const (
	PartitionPolicy      = "policy"
	PartitionToolCatalog = "tool_catalog"
)

// WorldStateSection is a diffable prompt partition (N7). Stable ID + digest let
// Assemble skip unchanged bodies while still emitting receipts.
type WorldStateSection interface {
	ID() string
	Digest() string
	Render() string
}

// ReceiptDigestsEqual reports whether two receipt slices have the same
// Kind→Digest pairs (WorldState-style skip signal for unchanged partitions).
func ReceiptDigestsEqual(left, right []Receipt) bool {
	if len(left) != len(right) {
		return false
	}
	index := make(map[string]string, len(left))
	for _, receipt := range left {
		index[receipt.Kind] = receipt.Digest
	}
	for _, receipt := range right {
		if index[receipt.Kind] != receipt.Digest {
			return false
		}
		delete(index, receipt.Kind)
	}
	return len(index) == 0
}

func digestJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%v", value)))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// PolicySection snapshots mode/permission/granular for WorldState.
type PolicySection struct {
	Mode       string          `json:"mode"`
	Permission string          `json:"permission"`
	Granular   policy.Granular `json:"granular"`
}

func NewPolicySection(runtime *policy.Runtime) PolicySection {
	if runtime == nil {
		return PolicySection{}
	}
	return PolicySection{
		Mode: string(runtime.Mode), Permission: string(runtime.Permission),
		Granular: runtime.Granular,
	}
}

func (p PolicySection) ID() string { return PartitionPolicy }

func (p PolicySection) Digest() string { return digestJSON(p) }

func (p PolicySection) Render() string {
	var b strings.Builder
	b.WriteString("Policy snapshot:\n")
	b.WriteString(fmt.Sprintf("- mode=%s permission=%s\n", p.Mode, p.Permission))
	b.WriteString(fmt.Sprintf(
		"- granular: sandbox=%s rules=%s skills=%s mcp=%s\n",
		postureLabel(p.Granular.Sandbox), postureLabel(p.Granular.Rules),
		postureLabel(p.Granular.Skills), postureLabel(p.Granular.MCP),
	))
	return strings.TrimSpace(b.String())
}

func postureLabel(value policy.SurfacePosture) string {
	if value == "" {
		return "inherit"
	}
	return string(value)
}

// ToolCatalogEntry is one model-visible tool line for WorldState.
type ToolCatalogEntry struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Availability string `json:"availability"`
	Capability   string `json:"capability,omitempty"`
}

// ToolCatalogSection lists visible tools (including deferred) for cache-aware diffs.
type ToolCatalogSection struct {
	CatalogID         string             `json:"catalog_id,omitempty"`
	Generation        uint64             `json:"generation,omitempty"`
	CatalogDigest     string             `json:"catalog_digest,omitempty"`
	Entries           []ToolCatalogEntry `json:"entries"`
	Materialized      []string           `json:"materialized,omitempty"`
	DeferredAvailable int                `json:"deferred_available,omitempty"`
	Omitted           int                `json:"omitted,omitempty"`
}

func NewToolCatalogSection(registry *tool.Registry) ToolCatalogSection {
	if registry == nil {
		return ToolCatalogSection{}
	}
	descriptors := registry.Descriptors(tool.VisibleModel)
	entries := make([]ToolCatalogEntry, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.Availability == tool.AvailabilityUnavailable {
			continue
		}
		desc := strings.TrimSpace(descriptor.Description)
		if len(desc) > 120 {
			desc = desc[:117] + "..."
		}
		entries = append(entries, ToolCatalogEntry{
			Name: descriptor.Name, Description: desc,
			Availability: string(descriptor.Availability),
			Capability:   string(descriptor.Capability),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return ToolCatalogSection{Entries: entries}
}

func NewToolCatalogSectionFromSnapshot(
	snapshot tool.CatalogSnapshot,
	advertised map[string]bool,
) ToolCatalogSection {
	section := ToolCatalogSection{
		CatalogID: snapshot.CatalogID, Generation: snapshot.Generation,
		CatalogDigest: snapshot.Digest,
	}
	for _, entry := range snapshot.Entries() {
		if entry.Descriptor.Visibility != tool.VisibleModel ||
			entry.Descriptor.Availability == tool.AvailabilityUnavailable {
			continue
		}
		if entry.State == tool.CatalogEntryDeferred {
			section.DeferredAvailable++
		}
		if entry.State == tool.CatalogEntryMaterialized {
			section.Materialized = append(section.Materialized, entry.Name)
		}
		if !advertised[entry.Name] {
			section.Omitted++
			continue
		}
		desc := strings.TrimSpace(entry.Descriptor.Description)
		if len(desc) > 120 {
			desc = desc[:117] + "..."
		}
		section.Entries = append(section.Entries, ToolCatalogEntry{
			Name: entry.Name, Description: desc,
			Availability: string(entry.Descriptor.Availability),
			Capability:   string(entry.Descriptor.Capability),
		})
	}
	sort.Strings(section.Materialized)
	sort.Slice(section.Entries, func(i, j int) bool {
		return section.Entries[i].Name < section.Entries[j].Name
	})
	return section
}

// AssembleToolCatalog renders the volatile catalog tail after history.
func AssembleToolCatalog(
	section ToolCatalogSection,
	budget Budget,
) ([]provider.Message, Receipt) {
	text := section.Render()
	tokens := HeuristicTokenCounter{}
	retained, reason := retain(text, budget, len(truncationNotice), 0, tokens)
	receipt := newReceipt(
		PartitionToolCatalog, "session://tool-catalog", text, retained, reason, tokens,
	)
	if reason != "" {
		retained += truncationNotice
	}
	if strings.TrimSpace(retained) == "" {
		return nil, receipt
	}
	return []provider.Message{provider.TextMessage(provider.RoleSystem, retained)}, receipt
}

func (t ToolCatalogSection) ID() string { return PartitionToolCatalog }

func (t ToolCatalogSection) Digest() string { return digestJSON(t) }

func (t ToolCatalogSection) Render() string {
	if len(t.Entries) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(
		&b, "[tool_catalog id=%s generation=%d digest=%s]\n",
		t.CatalogID, t.Generation, t.CatalogDigest,
	)
	if len(t.Materialized) != 0 {
		fmt.Fprintf(&b, "materialized: %s\n", strings.Join(t.Materialized, ", "))
	}
	if t.DeferredAvailable > 0 || t.Omitted > 0 {
		fmt.Fprintf(
			&b, "deferred_available=%d omitted=%d; use tool_search to load omitted tools.\n",
			t.DeferredAvailable, t.Omitted,
		)
	}
	for _, entry := range t.Entries {
		line := fmt.Sprintf("- %s [%s]", entry.Name, entry.Availability)
		if entry.Description != "" {
			line += ": " + entry.Description
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// SectionDigestMap indexes previous receipts by Kind for skip decisions.
func SectionDigestMap(receipts []Receipt) map[string]string {
	index := make(map[string]string, len(receipts))
	for _, receipt := range receipts {
		index[receipt.Kind] = receipt.Digest
	}
	return index
}
