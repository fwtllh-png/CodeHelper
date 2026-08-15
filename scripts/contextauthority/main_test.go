package main

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestInspectFileCountsAuthorityAndRejectsMigrationPaths(t *testing.T) {
	source := []byte(`package fixture
import "example/contextstore"
type state struct {
	contextLedger *contextstore.Ledger
	PreviousReceipts []string
}
func RefreshMode() {}
`)
	file, err := parser.ParseFile(
		token.NewFileSet(),
		"fixture.go",
		source,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	var result report
	inspectFile("fixture.go", source, file, &result)
	optionsSource := []byte(`package promptcontext
type Options struct { Mode string }
`)
	optionsFile, err := parser.ParseFile(
		token.NewFileSet(),
		"context.go",
		optionsSource,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	inspectFile(
		"internal/runtime/agent/promptcontext/context.go",
		optionsSource,
		optionsFile,
		&result,
	)
	if result.AuthorityCount != 1 ||
		len(result.AuthorityOwners) != 1 ||
		len(result.ForbiddenSymbols) != 3 {
		t.Fatalf("result=%+v", result)
	}
}
