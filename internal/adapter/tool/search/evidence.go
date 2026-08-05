package search

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
)

// maxEvidenceHits bounds the hits one call contributes to the caller's ledger. A
// grep can legitimately return hundreds of lines; the ledger exists to remember
// where the answer was, not to mirror the result.
const maxEvidenceHits = 32

// classifyPath labels a hit that carries no better information than its path.
// The classification is lexical, so it can only recognize what naming
// conventions reveal; anything else is a plain text match.
func classifyPath(path string) string {
	switch {
	case repoindex.IsTestPath(path):
		return tool.EvidenceTest
	case repoindex.IsConfigPath(path):
		return tool.EvidenceConfig
	default:
		return tool.EvidenceTextMatch
	}
}

// pathHits classifies whole-file hits, one entry per path.
func pathHits(paths []string) []tool.EvidenceHit {
	hits := make([]tool.EvidenceHit, 0, min(len(paths), maxEvidenceHits))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if _, found := seen[path]; found {
			continue
		}
		seen[path] = struct{}{}
		if len(hits) == maxEvidenceHits {
			break
		}
		hits = append(hits, tool.EvidenceHit{Kind: classifyPath(path), Path: path})
	}
	return hits
}

// textHits classifies content hits, one entry per file carrying its first
// matching line. A second hit in the same file would add a line number that says
// no more about where the answer lives.
func textHits(matches []textMatch) []tool.EvidenceHit {
	hits := make([]tool.EvidenceHit, 0, min(len(matches), maxEvidenceHits))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if _, found := seen[match.File]; found {
			continue
		}
		seen[match.File] = struct{}{}
		if len(hits) == maxEvidenceHits {
			break
		}
		hits = append(hits, tool.EvidenceHit{
			Kind: classifyPath(match.File), Path: match.File, Line: match.Line,
		})
	}
	return hits
}

// attach adds hits to metadata, leaving it untouched when there is nothing to
// classify: an absent key means "this tool reported no evidence", which is not
// the same as an empty list.
func attach(metadata map[string]any, hits []tool.EvidenceHit) map[string]any {
	if len(hits) == 0 {
		return metadata
	}
	if metadata == nil {
		metadata = make(map[string]any, 1)
	}
	metadata[tool.MetadataEvidence] = hits
	return metadata
}
