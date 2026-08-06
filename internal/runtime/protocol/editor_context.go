package protocol

import (
	"encoding/hex"
	"errors"
	"strings"
)

type EditorContextKind string

const (
	EditorContextFile        EditorContextKind = "file"
	EditorContextSelection   EditorContextKind = "selection"
	EditorContextSymbol      EditorContextKind = "symbol"
	EditorContextDiagnostics EditorContextKind = "diagnostics"
)

type EditorContextSource string

const (
	EditorContextSourceComposer         EditorContextSource = "composer"
	EditorContextSourceSelectionCommand EditorContextSource = "selection_command"
	EditorContextSourceCodeAction       EditorContextSource = "code_action"
)

type EditorPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type EditorRange struct {
	Start EditorPosition `json:"start"`
	End   EditorPosition `json:"end"`
}

type EditorSymbol struct {
	Name           string       `json:"name"`
	Kind           string       `json:"kind"`
	SelectionRange *EditorRange `json:"selection_range,omitempty"`
}

type EditorDiagnostic struct {
	Range    EditorRange `json:"range"`
	Severity string      `json:"severity"`
	Code     string      `json:"code,omitempty"`
	Message  string      `json:"message"`
	Source   string      `json:"source,omitempty"`
}

type EditorContextReference struct {
	Kind               EditorContextKind   `json:"kind"`
	Source             EditorContextSource `json:"source,omitempty"`
	URI                string              `json:"uri"`
	Path               string              `json:"path"`
	DocumentVersion    int                 `json:"document_version"`
	Digest             string              `json:"digest"`
	Range              *EditorRange        `json:"range,omitempty"`
	Symbol             *EditorSymbol       `json:"symbol,omitempty"`
	Diagnostics        []EditorDiagnostic  `json:"diagnostics,omitempty"`
	OmittedDiagnostics int                 `json:"omitted_diagnostics,omitempty"`
	Explicit           bool                `json:"explicit"`
}

func (r EditorContextReference) validate() error {
	switch r.Kind {
	case EditorContextFile, EditorContextSelection,
		EditorContextSymbol, EditorContextDiagnostics:
	default:
		return errors.New("unsupported editor context kind")
	}
	if r.URI == "" || r.Path == "" || r.DocumentVersion < 1 || !validSHA256(r.Digest) {
		return errors.New("editor context uri, path, document version, and digest are required")
	}
	if len(r.URI) > 4096 || len(r.Path) > 4096 {
		return errors.New("editor context uri or path exceeds its size limit")
	}
	if !r.Explicit {
		return errors.New("editor context must be explicitly selected")
	}
	if r.Source != "" && !validEditorContextSource(r.Source) {
		return errors.New("editor context source is invalid")
	}
	if (r.Kind == EditorContextSymbol || r.Kind == EditorContextDiagnostics) && r.Source == "" {
		return errors.New("symbol and diagnostics context require a source")
	}
	switch r.Kind {
	case EditorContextFile:
		if r.Range != nil || r.Symbol != nil || len(r.Diagnostics) != 0 ||
			r.OmittedDiagnostics != 0 {
			return errors.New("file context cannot carry range, symbol, or diagnostics")
		}
	case EditorContextSelection:
		if r.Range == nil || r.Symbol != nil || len(r.Diagnostics) != 0 ||
			r.OmittedDiagnostics != 0 {
			return errors.New("selection context requires only a range")
		}
	case EditorContextSymbol:
		if r.Range == nil || r.Symbol == nil || len(r.Diagnostics) != 0 ||
			r.OmittedDiagnostics != 0 {
			return errors.New("symbol context requires only a range and symbol")
		}
	case EditorContextDiagnostics:
		if r.Range != nil || r.Symbol != nil || len(r.Diagnostics) == 0 ||
			len(r.Diagnostics) > 32 || r.OmittedDiagnostics < 0 ||
			r.OmittedDiagnostics > 1_000_000 {
			return errors.New("diagnostics context requires between 1 and 32 diagnostics")
		}
	}
	if r.Range != nil && !validEditorRange(*r.Range, true) {
		return errors.New("editor context range is invalid or empty")
	}
	if r.Symbol != nil {
		if len(r.Symbol.Name) == 0 || len(r.Symbol.Name) > 512 ||
			len(r.Symbol.Kind) == 0 || len(r.Symbol.Kind) > 128 ||
			(r.Symbol.SelectionRange != nil &&
				(!validEditorRange(*r.Symbol.SelectionRange, true) ||
					!editorRangeContains(*r.Range, *r.Symbol.SelectionRange))) {
			return errors.New("editor context symbol is invalid")
		}
	}
	for _, diagnostic := range r.Diagnostics {
		if !validEditorRange(diagnostic.Range, false) ||
			!validDiagnosticSeverity(diagnostic.Severity) ||
			len(diagnostic.Message) == 0 || len(diagnostic.Message) > 8192 ||
			len(diagnostic.Code) > 256 || len(diagnostic.Source) > 256 {
			return errors.New("editor context diagnostic is invalid")
		}
	}
	return nil
}

func validEditorContextSource(value EditorContextSource) bool {
	switch value {
	case EditorContextSourceComposer, EditorContextSourceSelectionCommand,
		EditorContextSourceCodeAction:
		return true
	default:
		return false
	}
}

func validEditorRange(value EditorRange, requireNonEmpty bool) bool {
	start, end := value.Start, value.End
	if start.Line < 0 || start.Character < 0 || end.Line < 0 || end.Character < 0 ||
		end.Line < start.Line ||
		(end.Line == start.Line && end.Character < start.Character) {
		return false
	}
	return !requireNonEmpty || start != end
}

func editorRangeContains(outer, inner EditorRange) bool {
	return compareEditorPosition(outer.Start, inner.Start) <= 0 &&
		compareEditorPosition(inner.End, outer.End) <= 0
}

func compareEditorPosition(left, right EditorPosition) int {
	if left.Line != right.Line {
		return left.Line - right.Line
	}
	return left.Character - right.Character
}

func validDiagnosticSeverity(value string) bool {
	switch value {
	case "error", "warning", "information", "hint":
		return true
	default:
		return false
	}
}

type EditorContextReceipt struct {
	Kind               EditorContextKind   `json:"kind"`
	Source             EditorContextSource `json:"source,omitempty"`
	Path               string              `json:"path"`
	Digest             string              `json:"digest"`
	Range              *EditorRange        `json:"range,omitempty"`
	Symbol             *EditorSymbol       `json:"symbol,omitempty"`
	DiagnosticCount    int                 `json:"diagnostic_count,omitempty"`
	OmittedDiagnostics int                 `json:"omitted_diagnostics,omitempty"`
	OriginalBytes      int                 `json:"original_bytes"`
	RetainedBytes      int                 `json:"retained_bytes"`
	Truncated          bool                `json:"truncated,omitempty"`
}

func validateEditorContextReceipts(values []EditorContextReceipt) error {
	for _, value := range values {
		switch value.Kind {
		case EditorContextFile, EditorContextSelection,
			EditorContextSymbol, EditorContextDiagnostics:
		default:
			return errors.New("editor context receipt kind is invalid")
		}
		if value.Source != "" && !validEditorContextSource(value.Source) {
			return errors.New("editor context receipt source is invalid")
		}
		if value.Path == "" || len(value.Path) > 4096 || !validSHA256(value.Digest) ||
			value.OriginalBytes < 0 || value.RetainedBytes < 0 ||
			value.RetainedBytes > value.OriginalBytes ||
			value.DiagnosticCount < 0 || value.DiagnosticCount > 32 ||
			value.OmittedDiagnostics < 0 || value.OmittedDiagnostics > 1_000_000 ||
			(value.Truncated && value.RetainedBytes >= value.OriginalBytes) ||
			(!value.Truncated && value.RetainedBytes != value.OriginalBytes) {
			return errors.New("editor context receipt fields are invalid")
		}
		if value.Range != nil && !validEditorRange(*value.Range, true) {
			return errors.New("editor context receipt range is invalid")
		}
		if value.Symbol != nil &&
			(len(value.Symbol.Name) == 0 || len(value.Symbol.Name) > 512 ||
				len(value.Symbol.Kind) == 0 || len(value.Symbol.Kind) > 128 ||
				(value.Symbol.SelectionRange != nil &&
					(value.Range == nil ||
						!validEditorRange(*value.Symbol.SelectionRange, true) ||
						!editorRangeContains(*value.Range, *value.Symbol.SelectionRange)))) {
			return errors.New("editor context receipt symbol is invalid")
		}
		switch value.Kind {
		case EditorContextFile:
			if value.Range != nil || value.Symbol != nil ||
				value.DiagnosticCount != 0 || value.OmittedDiagnostics != 0 {
				return errors.New("file context receipt contains native metadata")
			}
		case EditorContextSelection:
			if value.Range == nil || value.Symbol != nil ||
				value.DiagnosticCount != 0 || value.OmittedDiagnostics != 0 {
				return errors.New("selection context receipt is invalid")
			}
		case EditorContextSymbol:
			if value.Source == "" || value.Range == nil || value.Symbol == nil ||
				value.DiagnosticCount != 0 || value.OmittedDiagnostics != 0 {
				return errors.New("symbol context receipt is invalid")
			}
		case EditorContextDiagnostics:
			if value.Source == "" || value.Range != nil || value.Symbol != nil ||
				value.DiagnosticCount < 1 {
				return errors.New("diagnostics context receipt is invalid")
			}
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
