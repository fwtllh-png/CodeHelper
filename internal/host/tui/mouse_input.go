package tui

import (
	"regexp"
	"strings"
)

var (
	sgrMouseReportPattern = regexp.MustCompile(
		`(?:\x1b\[|\[)?<\d{1,3};\d{1,5};\d{1,5}[mM]`,
	)
	leadingMouseFragmentPattern = regexp.MustCompile(
		`^(?:\x1b\[|\[)?[0-9;]{1,24}[mM]`,
	)
	trailingMouseFragmentPattern = regexp.MustCompile(
		`(?:\x1b\[|\[)?[0-9;]{1,24}[mM]$`,
	)
	mouseFragmentOnlyPattern = regexp.MustCompile(
		`^(?:(?:\x1b\[|\[)?[0-9;]*[mM]?)?$`,
	)
)

// stripMouseReportArtifacts removes SGR mouse wire reports that Bubble Tea can
// expose as KeyRunes when a rapid wheel burst is split across short reads.
// incoming preserves the KeyRunes boundary: a chunk made only of mouse reports
// is wire evidence even when Bubble Tea already consumed its ESC[ prefix.
func stripMouseReportArtifacts(value, incoming string) (string, bool) {
	matches := sgrMouseReportPattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value, false
	}
	hasWirePrefix := strings.Contains(value, "\x1b[<") || strings.Contains(value, "[<")
	incomingWithoutReports := sgrMouseReportPattern.ReplaceAllString(incoming, "")
	incomingIsMouse := len(sgrMouseReportPattern.FindAllStringIndex(incoming, -1)) != 0 &&
		mouseFragmentOnlyPattern.MatchString(incomingWithoutReports)
	cleaned := sgrMouseReportPattern.ReplaceAllString(value, "")
	mouseOnly := mouseFragmentOnlyPattern.MatchString(cleaned)
	if !hasWirePrefix && !incomingIsMouse && len(matches) == 1 && !mouseOnly {
		return value, false
	}
	cleaned = leadingMouseFragmentPattern.ReplaceAllString(cleaned, "")
	cleaned = trailingMouseFragmentPattern.ReplaceAllString(cleaned, "")
	return cleaned, true
}
