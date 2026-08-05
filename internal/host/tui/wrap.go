package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// wrapAwareLine soft-wraps s to width, preferring breaks inside URLs and
// absolute paths at /, ?, &, #, and path-segment dots.
func wrapAwareLine(s string, width int) string {
	if width < 8 {
		width = 8
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.Contains(s, "\n") && displayWidth(s) <= width {
		return s
	}
	var out strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(wrapOneLine(line, width))
	}
	return out.String()
}

func wrapOneLine(line string, width int) string {
	if displayWidth(line) <= width {
		return line
	}
	var b strings.Builder
	remaining := line
	first := true
	for remaining != "" {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		chunk, rest := takeWrapChunk(remaining, width)
		b.WriteString(chunk)
		remaining = rest
	}
	return b.String()
}

func takeWrapChunk(s string, width int) (chunk, rest string) {
	if displayWidth(s) <= width {
		return s, ""
	}
	// Prefer whitespace break within width.
	bestWS := -1
	bestHard := -1
	w := 0
	byteIdx := 0
	for byteIdx < len(s) {
		r, size := utf8.DecodeRuneInString(s[byteIdx:])
		rw := 1
		if r == utf8.RuneError && size == 1 {
			rw = 1
		} else if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			rw = 2
		}
		if w+rw > width {
			break
		}
		w += rw
		byteIdx += size
		ch := s[byteIdx-size : byteIdx]
		if r == ' ' || r == '\t' {
			bestWS = byteIdx
		}
		if isHardBreakRune(r) && inURLOrPathContext(s, byteIdx-size) {
			bestHard = byteIdx
		}
		_ = ch
	}
	cut := byteIdx
	if bestWS > 0 {
		cut = bestWS
	} else if bestHard > 0 {
		cut = bestHard
	}
	if cut <= 0 {
		cut = byteIdx
	}
	if cut <= 0 {
		_, size := utf8.DecodeRuneInString(s)
		if size == 0 {
			size = 1
		}
		cut = size
	}
	return s[:cut], strings.TrimLeft(s[cut:], " \t")
}

func isHardBreakRune(r rune) bool {
	switch r {
	case '/', '?', '&', '#':
		return true
	case '.':
		return true
	default:
		return false
	}
}

func inURLOrPathContext(s string, at int) bool {
	// Look left for http(s):// or path root.
	start := at
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:start])
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
		start -= size
	}
	token := s[start:]
	if i := strings.IndexAny(token, " \t\n"); i >= 0 {
		token = token[:i]
	}
	lower := strings.ToLower(token)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return true
	}
	if strings.HasPrefix(token, "/") || strings.HasPrefix(token, "~/") {
		return true
	}
	return false
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}
