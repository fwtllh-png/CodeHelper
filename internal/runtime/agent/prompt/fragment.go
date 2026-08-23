package prompt

import (
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

// MaxFragmentTokens is the hard per-fragment token ceiling. Each contextual
// injection is limited to approximately 10K tokens.
const MaxFragmentTokens = 10_000

// FragmentKind identifies a marked contextual injection that can be filtered
// and reinjected after compaction.
type FragmentKind string

const (
	FragmentConstitution FragmentKind = "constitution"
)

const (
	markerConstitutionStart = "<codehelper_fragment kind=\"constitution\">"
	markerConstitutionEnd   = "</codehelper_fragment>"
)

// FragmentMarkers returns start/end markers for a contextual fragment kind.
func FragmentMarkers(kind FragmentKind) (start, end string) {
	switch kind {
	case FragmentConstitution:
		return markerConstitutionStart, markerConstitutionEnd
	default:
		return "", ""
	}
}

// WrapFragment surrounds body with kind markers. Empty body stays empty.
func WrapFragment(kind FragmentKind, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	start, end := FragmentMarkers(kind)
	if start == "" {
		return body
	}
	return start + "\n" + body + "\n" + end
}

// MatchFragment reports whether text is a marked contextual fragment.
func MatchFragment(text string) (FragmentKind, bool) {
	trimmed := strings.TrimSpace(text)
	for _, kind := range []FragmentKind{FragmentConstitution} {
		start, end := FragmentMarkers(kind)
		if start == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(start)) &&
			strings.HasSuffix(strings.ToLower(trimmed), strings.ToLower(end)) {
			return kind, true
		}
	}
	return "", false
}

// IsContextualFragment reports whether text matches any known fragment markers.
func IsContextualFragment(text string) bool {
	_, ok := MatchFragment(text)
	return ok
}

// StripContextualFragments removes marked contextual injections from history so
// compact summaries do not embed stale constitution copies. Fresh static
// fragments are projected by ContextLedger on every sample.
func StripContextualFragments(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return messages
	}
	result := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		if IsContextualFragment(message.Text()) {
			continue
		}
		result = append(result, message)
	}
	return result
}

// ApplyFragmentTokenCeiling clamps MaxTokens for fragment partitions.
func ApplyFragmentTokenCeiling(budget Budget) Budget {
	if budget.MaxTokens == 0 || budget.MaxTokens > MaxFragmentTokens {
		budget.MaxTokens = MaxFragmentTokens
	}
	return budget
}
