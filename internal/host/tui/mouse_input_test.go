package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStripMouseReportArtifacts(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     string
		wantDrop bool
	}{
		{
			name: "complete wire report", input: "\x1b[<65;107;45M",
			want: "", wantDrop: true,
		},
		{
			name:  "fragmented wheel burst",
			input: "7;45M<65;107;45M<64;107;44M<65;107;45M",
			want:  "", wantDrop: true,
		},
		{
			name:  "burst inside text",
			input: "before<65;107;45M<64;107;44Mafter",
			want:  "beforeafter", wantDrop: true,
		},
		{
			name:  "single report shaped user text",
			input: "coordinate <65;107;45M",
			want:  "coordinate <65;107;45M",
		},
		{name: "normal text", input: "hello; world", want: "hello; world"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, dropped := stripMouseReportArtifacts(test.input, test.input)
			if got != test.want || dropped != test.wantDrop {
				t.Fatalf(
					"stripMouseReportArtifacts(%q) = (%q, %v), want (%q, %v)",
					test.input, got, dropped, test.want, test.wantDrop,
				)
			}
		})
	}
}

func TestComposerDropsSplitMouseWheelReports(t *testing.T) {
	model := NewModel(Options{}, &fakeRuntime{})
	model = model.withComposerText("draft")

	updated, _ := model.Update(tea.KeyMsg{
		Type: tea.KeyRunes, Runes: []rune("7;45M"),
	})
	model = updated.(Model)
	if !strings.HasSuffix(model.composerText(), "7;45M") {
		t.Fatalf("first fragment was not retained for boundary test: %q", model.composerText())
	}

	updated, _ = model.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("<65;107;45M<64;107;44M"),
	})
	model = updated.(Model)
	if got := model.composerText(); got != "draft" {
		t.Fatalf("composer = %q, want existing text only", got)
	}
}

func TestComposerDropsSinglePrefixlessMouseReportAfterSplit(t *testing.T) {
	model := NewModel(Options{}, &fakeRuntime{})
	model = model.withComposerText("draft")

	updated, _ := model.Update(tea.KeyMsg{
		Type: tea.KeyRunes, Runes: []rune("7;45M"),
	})
	model = updated.(Model)

	updated, _ = model.Update(tea.KeyMsg{
		Type: tea.KeyRunes, Runes: []rune("<65;107;45M"),
	})
	model = updated.(Model)
	if got := model.composerText(); got != "draft" {
		t.Fatalf("composer = %q, want existing text only", got)
	}
}

func TestComposerDropsIndividuallyDeliveredMouseReports(t *testing.T) {
	model := NewModel(Options{}, &fakeRuntime{})
	model = model.withComposerText("draft")

	for _, report := range []string{
		"<64;91;13M",
		"<65;107;45M",
		"<64;107;44M",
	} {
		updated, _ := model.Update(tea.KeyMsg{
			Type: tea.KeyRunes, Runes: []rune(report),
		})
		model = updated.(Model)
	}
	if got := model.composerText(); got != "draft" {
		t.Fatalf("composer = %q, want existing text only", got)
	}
}

func TestComposerPreservesReportShapedUserText(t *testing.T) {
	model := NewModel(Options{}, &fakeRuntime{})

	const input = "coordinate <65;107;45M"
	updated, _ := model.Update(tea.KeyMsg{
		Type: tea.KeyRunes, Runes: []rune(input),
	})
	model = updated.(Model)
	if got := model.composerText(); got != input {
		t.Fatalf("composer = %q, want %q", got, input)
	}
}
