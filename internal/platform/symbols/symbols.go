// Package symbols finds the declarations in a source file by reading its lines.
//
// It is deliberately lexical, not semantic: there is no parser, no type
// resolution and no build. That buys support for every language from one small
// rule table and keeps indexing a whole repository cheap, at the cost of the
// errors a lexer makes — a declaration written inside a string literal is
// reported, a macro that generates one is not. Consumers must label results
// accordingly rather than present them as ground truth.
package symbols

import (
	"strings"
	"unicode"
)

// Kinds a declaration can have. The vocabulary is shared across languages so a
// caller can filter without knowing which language produced a row: a Rust trait
// and a TypeScript interface are both KindInterface, a Go struct and a Rust enum
// are both KindType.
const (
	KindFunction  = "function"
	KindMethod    = "method"
	KindType      = "type"
	KindClass     = "class"
	KindInterface = "interface"
	KindConst     = "const"
	KindVar       = "var"
)

// Languages this package can extract declarations from.
const (
	LanguageGo         = "go"
	LanguagePython     = "python"
	LanguageJavaScript = "javascript"
	LanguageTypeScript = "typescript"
	LanguageRust       = "rust"
	LanguageJava       = "java"
)

// Symbol is one declaration. Line is 1-based so it can be shown to a reader
// without adjustment.
type Symbol struct {
	Name      string
	Kind      string
	Container string
	Line      int
	Exported  bool
}

// maxLines bounds how much of a file is scanned. Generated files run to hundreds
// of thousands of lines and contribute little worth indexing; stopping keeps one
// pathological file from dominating a refresh.
const maxLines = 50000

// languagesByExtension maps a file extension to its language. Extensions absent
// here are still indexed as files, only without declarations.
var languagesByExtension = map[string]string{
	".go":   LanguageGo,
	".py":   LanguagePython,
	".pyi":  LanguagePython,
	".js":   LanguageJavaScript,
	".jsx":  LanguageJavaScript,
	".mjs":  LanguageJavaScript,
	".cjs":  LanguageJavaScript,
	".ts":   LanguageTypeScript,
	".tsx":  LanguageTypeScript,
	".mts":  LanguageTypeScript,
	".rs":   LanguageRust,
	".java": LanguageJava,
}

// extractors holds one line scanner per supported language.
var extractors = map[string]func([]line) []Symbol{
	LanguageGo:         extractGo,
	LanguagePython:     extractPython,
	LanguageJavaScript: extractBraces,
	LanguageTypeScript: extractBraces,
	LanguageRust:       extractRust,
	LanguageJava:       extractJava,
}

// Language returns the language of path, or an empty string when the extension
// is unknown.
func Language(path string) string {
	lowered := strings.ToLower(path)
	if index := strings.LastIndexByte(lowered, '.'); index >= 0 {
		return languagesByExtension[lowered[index:]]
	}
	return ""
}

// Supported reports whether declarations can be extracted for a language.
func Supported(language string) bool {
	_, found := extractors[language]
	return found
}

// Extract returns the declarations of data, in the order they appear. An
// unsupported language yields no symbols rather than an error: the file is still
// worth indexing, it just has nothing to declare.
func Extract(language string, data []byte) []Symbol {
	extractor, found := extractors[language]
	if !found {
		return nil
	}
	return extractor(scan(language, data))
}

// line is one source line with its comment and string noise removed.
type line struct {
	// Number is the 1-based position in the original file.
	Number int
	// Code is the line with comment-only content blanked out. Trailing comments
	// are kept: cutting them would need to know which quotes are strings, and
	// getting that wrong corrupts the declaration itself.
	Code string
	// Indent is the count of leading spaces, with a tab counting as one.
	Indent int
}

// scan splits data into lines and blanks the ones that carry no code, so a
// declaration written inside a comment or a docstring is not reported.
func scan(language string, data []byte) []line {
	raw := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(raw) > maxLines {
		raw = raw[:maxLines]
	}
	lines := make([]line, 0, len(raw))
	inBlockComment := false
	docstring := ""
	for index, text := range raw {
		trimmed := strings.TrimSpace(text)
		code := text
		switch {
		case language == LanguagePython && docstring != "":
			code = ""
			if strings.Contains(trimmed, docstring) {
				docstring = ""
			}
		case language == LanguagePython && isDocstringStart(trimmed):
			// A docstring opening on its own line hides everything until it closes.
			code = ""
			if marker := docstringMarker(trimmed); !closesOnSameLine(trimmed, marker) {
				docstring = marker
			}
		case inBlockComment:
			code = ""
			if strings.Contains(text, "*/") {
				inBlockComment = false
				if after := text[strings.Index(text, "*/")+2:]; strings.TrimSpace(after) != "" {
					code = after
				}
			}
		case strings.HasPrefix(trimmed, "//"):
			code = ""
		case language == LanguagePython && strings.HasPrefix(trimmed, "#"):
			// Only Python comments start with #. Treating it as a comment
			// everywhere would hide a JavaScript private member.
			code = ""
		case strings.HasPrefix(trimmed, "/*"):
			code = ""
			if !strings.Contains(trimmed, "*/") {
				inBlockComment = true
			}
		}
		lines = append(lines, line{Number: index + 1, Code: code, Indent: indentOf(code)})
	}
	return lines
}

func isDocstringStart(trimmed string) bool {
	return strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, "'''") ||
		strings.HasPrefix(trimmed, `r"""`) || strings.HasPrefix(trimmed, `f"""`)
}

func docstringMarker(trimmed string) string {
	if strings.Contains(trimmed, `"""`) {
		return `"""`
	}
	return "'''"
}

func closesOnSameLine(trimmed, marker string) bool {
	body := trimmed[strings.Index(trimmed, marker)+len(marker):]
	return strings.Contains(body, marker)
}

func indentOf(text string) int {
	count := 0
	for _, symbol := range text {
		switch symbol {
		case ' ', '\t':
			count++
		default:
			return count
		}
	}
	return count
}

// exportedByCase reports the Go and Java convention that a capitalised name is
// visible outside its package.
func exportedByCase(name string) bool {
	for _, symbol := range name {
		return unicode.IsUpper(symbol)
	}
	return false
}

// container tracks the declaration a brace language is currently inside. opened
// distinguishes a body that has started from one whose brace is still on a later
// line, so a type declared in the Allman style keeps its members.
type container struct {
	name   string
	depth  int
	opened bool
}

// scope follows brace nesting so a method can report the type that holds it.
type scope struct {
	depth      int
	containers []container
}

// enter records a container whose body opens at the current depth.
func (s *scope) enter(name string) {
	s.containers = append(s.containers, container{name: name, depth: s.depth})
}

// current is the innermost container, or an empty string at top level.
func (s *scope) current() string {
	if len(s.containers) == 0 {
		return ""
	}
	return s.containers[len(s.containers)-1].name
}

// advance applies one line's braces and drops containers whose body ended.
func (s *scope) advance(code string) {
	s.depth += strings.Count(code, "{") - strings.Count(code, "}")
	if s.depth < 0 {
		s.depth = 0
	}
	for index := range s.containers {
		if s.depth > s.containers[index].depth {
			s.containers[index].opened = true
		}
	}
	for len(s.containers) != 0 {
		innermost := s.containers[len(s.containers)-1]
		if !innermost.opened || s.depth > innermost.depth {
			break
		}
		s.containers = s.containers[:len(s.containers)-1]
	}
}
