package turnkernel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestCommandMatrixCoversEveryCommand(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "command.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := make(map[string]bool)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != "commandName" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			name, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr == nil {
				declared[name] = true
			}
			return false
		})
	}
	covered := make(map[string]bool)
	for _, contract := range CommandContracts() {
		if contract.Name == "" || contract.Family == "" {
			t.Fatalf("invalid command contract: %+v", contract)
		}
		if covered[contract.Name] {
			t.Fatalf("duplicate command contract %q", contract.Name)
		}
		covered[contract.Name] = true
	}
	for name := range declared {
		if !covered[name] {
			t.Errorf("command %q has no matrix contract", name)
		}
	}
	for name := range covered {
		if !declared[name] {
			t.Errorf("matrix contains unknown command %q", name)
		}
	}
}

func TestCommandContractsReturnsIsolatedCopy(t *testing.T) {
	first := CommandContracts()
	first[0].AllowedPhases[0] = PhaseFailed
	if got := CommandContracts()[0].AllowedPhases[0]; got != PhaseCreated {
		t.Fatalf("command matrix mutated through returned copy: %s", got)
	}
}
