package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type report struct {
	Status           string   `json:"status"`
	AuthorityCount   int      `json:"authority_count"`
	AuthorityOwners  []string `json:"authority_owners"`
	ForbiddenSymbols []string `json:"forbidden_symbols,omitempty"`
	FilesScanned     int      `json:"files_scanned"`
}

var forbiddenDeclarations = map[string]struct{}{
	"TurnContextSnapshot": {},
	"RefreshMode":         {},
	"SectionDigestMap":    {},
	"promptSections":      {},
}

var forbiddenFields = map[string]struct{}{
	"PreviousReceipts":  {},
	"ModePromptBudget":  {},
	"ToolCatalogBudget": {},
	"PromptContext":     {},
}

var forbiddenPromptOptions = map[string]struct{}{
	"Mode":             {},
	"Plan":             {},
	"Skills":           {},
	"Sections":         {},
	"PreviousReceipts": {},
}

var forbiddenFragments = []string{
	"EnableContextLedger",
	"UseContextLedger",
	"LegacyContext",
	"DualWriteContext",
}

func main() {
	root := flag.String("root", ".", "repository root")
	output := flag.String("output", "", "optional JSON report path")
	flag.Parse()
	result, err := inspect(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded = append(encoded, '\n')
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(*output, encoded, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	_, _ = os.Stdout.Write(encoded)
	if result.Status != "passed" {
		os.Exit(1)
	}
}

func inspect(root string) (report, error) {
	result := report{Status: "passed"}
	files := filepath.Join(root, "internal")
	err := filepath.WalkDir(files, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		result.FilesScanned++
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(
			token.NewFileSet(),
			path,
			source,
			0,
		)
		if err != nil {
			return err
		}
		inspectFile(
			filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))),
			source,
			parsed,
			&result,
		)
		return nil
	})
	if err != nil {
		return report{}, err
	}
	sort.Strings(result.AuthorityOwners)
	sort.Strings(result.ForbiddenSymbols)
	if result.AuthorityCount != 1 || len(result.ForbiddenSymbols) != 0 {
		result.Status = "failed"
	}
	return result, nil
}

func inspectFile(path string, source []byte, file *ast.File, result *report) {
	for _, fragment := range forbiddenFragments {
		if strings.Contains(string(source), fragment) {
			result.ForbiddenSymbols = append(
				result.ForbiddenSymbols,
				path+":"+fragment,
			)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.TypeSpec:
			if _, forbidden := forbiddenDeclarations[value.Name.Name]; forbidden {
				result.ForbiddenSymbols = append(
					result.ForbiddenSymbols,
					path+":"+value.Name.Name,
				)
			}
			if path == "internal/runtime/agent/promptcontext/context.go" &&
				value.Name.Name == "Options" {
				inspectPromptOptions(path, value.Type, result)
			}
		case *ast.FuncDecl:
			if _, forbidden := forbiddenDeclarations[value.Name.Name]; forbidden {
				result.ForbiddenSymbols = append(
					result.ForbiddenSymbols,
					path+":"+value.Name.Name,
				)
			}
		case *ast.Field:
			for _, name := range value.Names {
				if _, forbidden := forbiddenFields[name.Name]; forbidden {
					result.ForbiddenSymbols = append(
						result.ForbiddenSymbols,
						path+":"+name.Name,
					)
				}
				if isContextLedger(value.Type) {
					result.AuthorityCount++
					result.AuthorityOwners = append(
						result.AuthorityOwners,
						path+":"+name.Name,
					)
				}
			}
		}
		return true
	})
}

func inspectPromptOptions(
	path string,
	value ast.Expr,
	result *report,
) {
	structure, ok := value.(*ast.StructType)
	if !ok {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if _, forbidden := forbiddenPromptOptions[name.Name]; forbidden {
				result.ForbiddenSymbols = append(
					result.ForbiddenSymbols,
					path+":Options."+name.Name,
				)
			}
		}
	}
}

func isContextLedger(value ast.Expr) bool {
	pointer, ok := value.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Ledger" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "contextstore"
}
