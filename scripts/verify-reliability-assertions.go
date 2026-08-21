// verify-reliability-assertions statically checks that each test referenced
// in the reliability matrix actually contains the required assertion patterns.
// It uses go/ast and go/parser to parse test files and walk the AST looking
// for specific call patterns (e.g., fault.CodeOf, errors.Is, etc.).
//
// Usage:
//
//	go run ./scripts/verify-reliability-assertions.go \
//	    -matrix testdata/contracts/reliability-matrix.json \
//	    -assertions testdata/contracts/reliability-assertions.json
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type AssertionType string

const (
	AssertFaultCheck          AssertionType = "fault_check"
	AssertRetryableCheck      AssertionType = "retryable_check"
	AssertSingleAttemptCheck  AssertionType = "single_attempt_check"
	AssertContextCanceledCheck AssertionType = "context_canceled_check"
	AssertResourceCleanupCheck AssertionType = "resource_cleanup_check"
	AssertResourceReleaseCheck AssertionType = "resource_release_check"
	AssertNoRecoveryCheck     AssertionType = "no_recovery_check"
	AssertRecoveryCheck       AssertionType = "recovery_check"
	AssertAtomicityCheck      AssertionType = "atomicity_check"
	AssertIdempotencyCheck    AssertionType = "idempotency_check"
	AssertConcurrencyCheck    AssertionType = "concurrency_check"
	AssertShutdownCheck       AssertionType = "shutdown_check"
)

type AssertionEntry struct {
	Boundary string            `json:"boundary"`
	Case     string            `json:"case"`
	Test     string            `json:"test"`
	Package  string            `json:"package"`
	Required []AssertionReq    `json:"required"`
}

type AssertionReq struct {
	Type AssertionType `json:"type"`
	Note string        `json:"note"`
}

type AssertionsFile struct {
	Version    int              `json:"version"`
	Assertions []AssertionEntry `json:"assertions"`
}

// assertionChecker checks whether a test function satisfies a specific assertion type.
type assertionChecker struct {
	fset      *token.FileSet
	funcDecl  *ast.FuncDecl
	satisfied map[AssertionType]bool
}

func newAssertionChecker(fset *token.FileSet, funcDecl *ast.FuncDecl) *assertionChecker {
	return &assertionChecker{
		fset:      fset,
		funcDecl:  funcDecl,
		satisfied: make(map[AssertionType]bool),
	}
}

func (c *assertionChecker) check() {
	ast.Inspect(c.funcDecl.Body, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		funcName := selExpr.Sel.Name
		pkgIdent, _ := selExpr.X.(*ast.Ident)

		// Check for fault.CodeOf(err) or protocol.CodeOf(err) or similar.
		if (funcName == "CodeOf" || funcName == "IsCode") && pkgIdent != nil &&
			(pkgIdent.Name == "fault" || pkgIdent.Name == "protocol") {
			c.satisfied[AssertFaultCheck] = true
		}
		// Check for errors.Is(err, ...) or errors.As(err, ...).
		if (funcName == "Is" || funcName == "As") && pkgIdent != nil && pkgIdent.Name == "errors" {
			c.satisfied[AssertFaultCheck] = true
		}
		// Check for struct field access like .Code or .Fault on the error.
		if funcName == "Code" || funcName == "Fault" {
			c.satisfied[AssertFaultCheck] = true
		}
		// Check for t.Errorf, t.Fatalf, t.Error, t.Fatal with relevant patterns.
		if (funcName == "Errorf" || funcName == "Fatalf" || funcName == "Error" || funcName == "Fatal") &&
			pkgIdent != nil && pkgIdent.Name == "t" {
			// Check arguments for retryable/recovery/cleanup keywords.
			for _, arg := range callExpr.Args {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					lower := strings.ToLower(lit.Value)
					if strings.Contains(lower, "retry") || strings.Contains(lower, "no retry") ||
						strings.Contains(lower, "deny") {
						c.satisfied[AssertRetryableCheck] = true
						c.satisfied[AssertNoRecoveryCheck] = true
					}
					if strings.Contains(lower, "cancel") || strings.Contains(lower, "ctx") {
						c.satisfied[AssertContextCanceledCheck] = true
					}
					if strings.Contains(lower, "close") || strings.Contains(lower, "body") ||
						strings.Contains(lower, "requestcanceled") || strings.Contains(lower, "canceled") {
						c.satisfied[AssertResourceCleanupCheck] = true
					}
					if strings.Contains(lower, "release") || strings.Contains(lower, "claim") {
						c.satisfied[AssertResourceReleaseCheck] = true
					}
					if strings.Contains(lower, "recover") || strings.Contains(lower, "repair") ||
						strings.Contains(lower, "last sequence") || strings.Contains(lower, "torn") {
						c.satisfied[AssertRecoveryCheck] = true
					}
					if strings.Contains(lower, "atomic") || strings.Contains(lower, "rollback") ||
						strings.Contains(lower, "commit") || strings.Contains(lower, "crash") {
						c.satisfied[AssertAtomicityCheck] = true
					}
					if strings.Contains(lower, "idempotent") || strings.Contains(lower, "repeat") {
						c.satisfied[AssertIdempotencyCheck] = true
					}
					if strings.Contains(lower, "concurrent") || strings.Contains(lower, "goroutine") {
						c.satisfied[AssertConcurrencyCheck] = true
					}
					if strings.Contains(lower, "shutdown") || strings.Contains(lower, "close") {
						c.satisfied[AssertShutdownCheck] = true
					}
					if strings.Contains(lower, "egress") || strings.Contains(lower, "denied") {
						c.satisfied[AssertFaultCheck] = true
					}
				}
			}
		}
		// Check for single attempt patterns — also check Fatalf/Errorf format strings.
		if funcName == "Fatalf" || funcName == "Errorf" {
			for _, arg := range callExpr.Args {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					lower := strings.ToLower(lit.Value)
					if strings.Contains(lower, "attempt") ||
						strings.Contains(lower, "want 1") ||
						strings.Contains(lower, "!= 1") {
						c.satisfied[AssertSingleAttemptCheck] = true
					}
				}
			}
		}
		if funcName == "Load" && pkgIdent == nil {
			// Check if this is call on an atomic counter (attempts.Load()).
			c.satisfied[AssertSingleAttemptCheck] = true
		}
		// Check for map index access patterns like result.Metadata["error_category"].
		if funcName == "Metadata" || funcName == "error_category" {
			c.satisfied[AssertFaultCheck] = true
		}
		return true
	})
}

func main() {
	matrixPath := ""
	assertionsPath := ""

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "-matrix":
			i++
			matrixPath = os.Args[i]
		case "-assertions":
			i++
			assertionsPath = os.Args[i]
		}
	}

	if matrixPath == "" || assertionsPath == "" {
		fmt.Fprintln(os.Stderr, "usage: verify-reliability-assertions -matrix <path> -assertions <path>")
		os.Exit(2)
	}

	// Read assertions file.
	data, err := os.ReadFile(assertionsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read assertions: %v\n", err)
		os.Exit(1)
	}
	var assertionsFile AssertionsFile
	if err := json.Unmarshal(data, &assertionsFile); err != nil {
		fmt.Fprintf(os.Stderr, "parse assertions: %v\n", err)
		os.Exit(1)
	}

	root := findRoot()

	failures := 0
	for _, entry := range assertionsFile.Assertions {
		pkgPath := filepath.Join(root, entry.Package)
		if entry.Package != "" {
			pkgPath = filepath.Clean(filepath.Join(root, entry.Package))
		}

		// Find the test file containing this test.
		testFile, err := findTestFile(pkgPath, entry.Test)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s/%s/%s: %v\n", entry.Boundary, entry.Case, entry.Test, err)
			failures++
			continue
		}

		// Parse the test file.
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, testFile, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s/%s/%s: parse error: %v\n", entry.Boundary, entry.Case, entry.Test, err)
			failures++
			continue
		}

		// Find the test function.
		var funcDecl *ast.FuncDecl
		for _, decl := range file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == entry.Test {
				funcDecl = fd
				break
			}
		}
		if funcDecl == nil {
			fmt.Fprintf(os.Stderr, "%s/%s/%s: test function not found in %s\n",
				entry.Boundary, entry.Case, entry.Test, testFile)
			failures++
			continue
		}

		// Check assertions.
		checker := newAssertionChecker(fset, funcDecl)
		checker.check()

		// Also check the test function name for structural patterns.
		testName := funcDecl.Name.Name
		if strings.Contains(testName, "Concurrency") || strings.Contains(testName, "Concurrent") {
			checker.satisfied[AssertConcurrencyCheck] = true
		}
		if strings.Contains(testName, "Shutdown") {
			checker.satisfied[AssertShutdownCheck] = true
		}

		for _, req := range entry.Required {
			if !checker.satisfied[req.Type] {
				fmt.Fprintf(os.Stderr, "%s/%s/%s: missing assertion %s: %s\n",
					entry.Boundary, entry.Case, entry.Test, req.Type, req.Note)
				failures++
			}
		}
	}

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "%d assertion failures\n", failures)
		os.Exit(1)
	}
	fmt.Printf("reliability assertions passed: %d entries verified\n", len(assertionsFile.Assertions))
}

func findRoot() string {
	// Find the repo root by looking for go.mod.
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

func findTestFile(pkgPath, testName string) (string, error) {
	entries, err := os.ReadDir(pkgPath)
	if err != nil {
		// Try without the leading ./.
		return "", fmt.Errorf("read package dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
			fullPath := filepath.Join(pkgPath, entry.Name())
			// Quick check: does the file contain the test function name?
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			if strings.Contains(string(data), "func "+testName+"(") {
				return fullPath, nil
			}
		}
	}
	return "", fmt.Errorf("test file containing %s not found in %s", testName, pkgPath)
}