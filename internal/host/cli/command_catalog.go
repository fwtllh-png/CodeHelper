package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// CommandDescriptor is the documentation-safe projection of one Cobra node.
type CommandDescriptor struct {
	Path    string
	Usage   string
	Summary string
}

// CommandCatalog returns every visible command registered below the root.
func CommandCatalog() []CommandDescriptor {
	code := 0
	root := newRoot(
		context.Background(),
		strings.NewReader(""),
		io.Discard,
		io.Discard,
		&code,
	)
	return commandCatalog(root)
}

func commandCatalog(root *cobra.Command) []CommandDescriptor {
	root.InitDefaultHelpCmd()
	descriptors := make([]CommandDescriptor, 0)
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, command := range parent.Commands() {
			if command.Hidden {
				continue
			}
			descriptors = append(descriptors, CommandDescriptor{
				Path:    command.CommandPath(),
				Usage:   command.UseLine(),
				Summary: command.Short,
			})
			walk(command)
		}
	}
	walk(root)
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].Path < descriptors[j].Path
	})
	return descriptors
}

func renderRootHelp(root *cobra.Command) string {
	var output bytes.Buffer
	_, _ = fmt.Fprintf(
		&output,
		"%s - %s\n\nUsage:\n  %s COMMAND [ARGS]\n  %s help\n\nCommands:\n",
		root.Name(),
		root.Short,
		root.Name(),
		root.Name(),
	)
	for _, command := range commandCatalog(root) {
		_, _ = fmt.Fprintf(
			&output,
			"  %-48s %s\n",
			command.Usage,
			command.Summary,
		)
	}
	return output.String()
}

// RenderCommandReference renders the generated command-list block for docs.
func RenderCommandReference(locale string) string {
	title := "## Generated Command List"
	note := "Generated from the Cobra command tree. Do not edit this block by hand."
	header := "| Command | Summary |\n| --- | --- |"
	if locale == "zh-CN" {
		title = "## 生成的命令清单"
		note = "此清单从 Cobra Command Tree 生成，请勿手工编辑此区块。"
		header = "| 命令 | 说明 |\n| --- | --- |"
	}

	var output bytes.Buffer
	_, _ = fmt.Fprintf(&output, "%s\n\n%s\n\n%s\n", title, note, header)
	for _, command := range CommandCatalog() {
		_, _ = fmt.Fprintf(
			&output,
			"| `%s` | %s |\n",
			command.Usage,
			escapeMarkdownTable(command.Summary),
		)
	}
	return strings.TrimSpace(output.String())
}

func escapeMarkdownTable(value string) string {
	return strings.ReplaceAll(value, "|", `\|`)
}
