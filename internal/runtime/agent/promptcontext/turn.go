package promptcontext

import (
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/repomap"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

// Partitions rebuilt on every sample rather than frozen at bootstrap.
const (
	PartitionRepoMap          = "repo_map"
	PartitionWorkingSetLedger = "working_set_ledger"
	PartitionEvidence         = "evidence"
)

// truncationNotice closes a section a budget cut short. It ends the section, so
// it survives a prefix-preserving cut only because its length is reserved.
const truncationNotice = "(this section was cut to fit its budget; " +
	"what is not listed here may still exist)\n"

// TurnOptions is the state a turn context is rendered from. Paths are
// workspace-relative, which is both what the repository index stores and what a
// model can hand back to a file tool unchanged.
type TurnOptions struct {
	Turn       uint64
	RepoMap    repomap.Map
	WorkingSet []workingset.Entry
	Evidence   evidence.Snapshot
	Budgets    map[string]Budget
	Tokens     TokenCounter
}

// TurnContext is the volatile tail of a request.
type TurnContext struct {
	Messages []provider.Message `json:"messages"`
	Receipts []Receipt          `json:"receipts"`
}

// TurnState is what the engine knows when a sample is about to go out. It is one
// struct rather than a growing parameter list because every consumer of the tail
// needs the same snapshot of the turn, and adding to it must not force every
// implementation of the tail to change shape.
type TurnState struct {
	Turn       uint64
	WorkingSet []workingset.Entry
	Evidence   evidence.Snapshot
}

// AssembleTurn renders the partitions that change while a session runs.
//
// The result belongs at the end of a request, after the history. Everything
// before it is then byte-identical from one sample to the next, which is what
// lets a provider serve the prefix from its cache; a volatile section placed in
// the middle would instead invalidate every message after it.
//
// It cannot fail. A section with nothing to say produces a receipt and no
// message, so the absence is still auditable.
func AssembleTurn(options TurnOptions) TurnContext {
	tokens := options.Tokens
	if tokens == nil {
		tokens = HeuristicTokenCounter{}
	}
	var result TurnContext
	appendSection := func(kind, text, sourcePath string) {
		// The notice is charged to the budget up front, so a section that runs out
		// of room can still admit it: a model that cannot tell a cut from an
		// absence will read the missing lines as missing code.
		retained, reason := retain(text, options.Budgets[kind], len(truncationNotice), 0, tokens)
		result.Receipts = append(
			result.Receipts,
			newReceipt(kind, sourcePath, text, retained, reason, tokens),
		)
		if reason != "" {
			retained += truncationNotice
		}
		if strings.TrimSpace(retained) != "" {
			result.Messages = append(
				result.Messages,
				provider.TextMessage(provider.RoleSystem, retained),
			)
		}
	}
	// A section the caller did not ask for is skipped whole, receipt included: a
	// zero Map or an empty ledger means "not requested", which is different from
	// "requested and empty" and should not read as a truncated section.
	if options.RepoMap.Status != "" {
		appendSection(PartitionRepoMap, renderRepoMap(options), "session://repo-map")
	}
	if len(options.WorkingSet) != 0 {
		appendSection(PartitionWorkingSetLedger, renderWorkingSet(options), "session://working-set")
	}
	if !options.Evidence.Empty() {
		appendSection(PartitionEvidence, renderEvidence(options), "session://evidence")
	}
	return result
}

// renderRepoMap describes the repository, or says why it cannot.
func renderRepoMap(options TurnOptions) string {
	built := options.RepoMap
	var b strings.Builder
	fmt.Fprintf(&b, "[repo_map turn=%d index=%s]\n", options.Turn, built.Status)
	if !built.Ready() {
		// Naming the reason matters more than the map: a model that reads "not
		// found" as "does not exist" will draw the wrong conclusion.
		b.WriteString(unavailableRepoMap(built))
		return b.String()
	}
	if built.FileCount == 0 {
		return ""
	}
	fmt.Fprintf(&b, "%d indexed files, %d declarations (lexical).\n", built.FileCount, built.SymbolCount)
	if len(built.Build) != 0 {
		fmt.Fprintf(&b, "build: %s\n", strings.Join(built.Build, ", "))
	}
	if len(built.Entries) != 0 {
		fmt.Fprintf(&b, "entry: %s\n", strings.Join(built.Entries, ", "))
	}
	if len(built.Directories) != 0 {
		b.WriteString("directories:\n")
		for _, directory := range built.Directories {
			fmt.Fprintf(&b, "  %s — %d files, %d declarations", directory.Path, directory.Files, directory.Symbols)
			if len(directory.Languages) != 0 {
				fmt.Fprintf(&b, ", %s", strings.Join(directory.Languages, "/"))
			}
			b.WriteByte('\n')
		}
		if built.OmittedDirectories > 0 {
			fmt.Fprintf(&b, "  (%d more directories not listed)\n", built.OmittedDirectories)
		}
	}
	if len(built.Outlines) != 0 {
		b.WriteString("declarations in the files below:\n")
		for _, outline := range built.Outlines {
			fmt.Fprintf(&b, "  %s\n", outline.Path)
			for _, symbol := range outline.Symbols {
				fmt.Fprintf(&b, "    %d %s %s", symbol.Line, symbol.Kind, symbol.Name)
				if symbol.Container != "" {
					fmt.Fprintf(&b, " (in %s)", symbol.Container)
				}
				b.WriteByte('\n')
			}
			if outline.Truncated {
				b.WriteString("    (more declarations not listed)\n")
			}
		}
	}
	return b.String()
}

// renderWorkingSet lists the paths in play. It carries names and provenance, not
// contents: what the agent read is already in the history, and re-sending it
// would be paid for on every sample.
func renderWorkingSet(options TurnOptions) string {
	if len(options.WorkingSet) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[working_set turn=%d]\n", options.Turn)
	b.WriteString(
		"Paths this session has touched, most relevant first. " +
			"Contents are not included; read what you need.\n",
	)
	for _, entry := range options.WorkingSet {
		fmt.Fprintf(&b, "  %s — %s", entry.Path, joinSources(entry.Sources))
		fmt.Fprintf(&b, " (turn %d)", entry.LastTurn)
		if entry.Critical {
			b.WriteString(" critical")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// renderEvidence reports what the thread knows and what it owes.
//
// The order is deliberate and the opposite of how the section was built:
// reminders and risks come before the facts because a budget cut keeps the
// prefix, and advice the model can act on now is worth more than another list of
// paths it has already seen.
func renderEvidence(options TurnOptions) string {
	snapshot := options.Evidence
	var b strings.Builder
	fmt.Fprintf(&b, "[evidence turn=%d]\n", options.Turn)
	if len(snapshot.Reminders) != 0 {
		b.WriteString("wasted effort:\n")
		for _, reminder := range snapshot.Reminders {
			fmt.Fprintf(&b, "  %s\n", reminder.Detail)
		}
	}
	if len(snapshot.Risks) != 0 {
		b.WriteString("unproved, and yours to close:\n")
		for _, risk := range snapshot.Risks {
			fmt.Fprintf(&b, "  %s — %s (turn %d)\n", risk.Path, riskLabel(risk.Kind), risk.Turn)
		}
	}
	if len(snapshot.Facts) != 0 {
		b.WriteString("what lookups established:\n")
		for _, fact := range snapshot.Facts {
			b.WriteString("  " + fact.Describe() + "\n")
		}
		if snapshot.OmittedFacts > 0 {
			fmt.Fprintf(&b, "  (%d more not listed)\n", snapshot.OmittedFacts)
		}
	}
	return b.String()
}

// riskLabel says what is missing in words, because a model asked to close a gap
// should not have to decode an identifier.
func riskLabel(kind string) string {
	switch kind {
	case evidence.RiskUnverifiedChange:
		return "changed, nothing verified it"
	case evidence.RiskBlindChange:
		return "changed without being read first"
	case evidence.RiskOpenDiagnostics:
		return "diagnostics still failing"
	default:
		return kind
	}
}

func joinSources(sources []workingset.Source) string {
	if len(sources) == 0 {
		return "touched"
	}
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, string(source))
	}
	return strings.Join(names, ", ")
}

func unavailableRepoMap(built repomap.Map) string {
	reason := strings.TrimSpace(built.Detail)
	if reason == "" {
		reason = "no detail was reported"
	}
	return fmt.Sprintf(
		"No repository map is available (%s). "+
			"A path missing from this section may still exist; use search_text to check.\n",
		reason,
	)
}
