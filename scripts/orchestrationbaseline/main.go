// Command orchestrationbaseline freezes lifecycle scheduler sites until the
// WorkGraph kernel retires them in later orchestration phases.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type contract struct {
	SchemaVersion int                  `json:"schema_version"`
	RequirementID string               `json:"requirement_id"`
	Roots         []string             `json:"roots"`
	Sites         map[string]siteLimit `json:"sites"`
}

type siteLimit struct {
	GoStatements  int `json:"go_statements,omitempty"`
	NewTickers    int `json:"new_tickers,omitempty"`
	AfterCalls    int `json:"after_calls,omitempty"`
	AfterFuncs    int `json:"after_funcs,omitempty"`
	Subscriptions int `json:"subscriptions,omitempty"`
}

type report struct {
	SchemaVersion int                  `json:"schema_version"`
	RequirementID string               `json:"requirement_id"`
	Sites         map[string]siteLimit `json:"sites"`
	Totals        siteLimit            `json:"totals"`
}

func main() {
	var root, baselinePath, reportPath string
	flag.StringVar(&root, "root", ".", "repository root")
	flag.StringVar(
		&baselinePath,
		"baseline",
		"docs/task-orchestration-or0-scheduler-baseline.json",
		"scheduler baseline",
	)
	flag.StringVar(&reportPath, "report", "", "optional report path")
	flag.Parse()

	result, err := run(root, baselinePath, reportPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf(
		"orchestration scheduler baseline passed: %d files, %d goroutines, %d timers, %d subscriptions\n",
		len(result.Sites),
		result.Totals.GoStatements,
		result.Totals.NewTickers+result.Totals.AfterCalls+result.Totals.AfterFuncs,
		result.Totals.Subscriptions,
	)
}

func run(root, baselinePath, reportPath string) (report, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return report{}, err
	}
	baseline, err := readContract(filepath.Join(absoluteRoot, filepath.FromSlash(baselinePath)))
	if err != nil {
		return report{}, err
	}
	if err := validateContract(baseline); err != nil {
		return report{}, err
	}
	result := report{
		SchemaVersion: 1,
		RequirementID: baseline.RequirementID,
		Sites:         make(map[string]siteLimit),
	}
	for _, configuredRoot := range baseline.Roots {
		if err := scanRoot(absoluteRoot, configuredRoot, result.Sites); err != nil {
			return report{}, err
		}
	}
	for path, site := range result.Sites {
		result.Totals = add(result.Totals, site)
		maximum, exists := baseline.Sites[path]
		if !exists {
			return result, fmt.Errorf(
				"orchestration scheduler drift: new lifecycle site %s: %+v",
				path,
				site,
			)
		}
		if failures := exceeds(site, maximum); len(failures) != 0 {
			return result, fmt.Errorf(
				"orchestration scheduler drift at %s: %s",
				path,
				strings.Join(failures, ", "),
			)
		}
	}
	if reportPath != "" {
		if err := writeReport(
			filepath.Join(absoluteRoot, filepath.FromSlash(reportPath)),
			result,
		); err != nil {
			return report{}, err
		}
	}
	return result, nil
}

func readContract(path string) (contract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return contract{}, err
	}
	var value contract
	if err := json.Unmarshal(data, &value); err != nil {
		return contract{}, fmt.Errorf("decode scheduler baseline: %w", err)
	}
	return value, nil
}

func validateContract(value contract) error {
	if value.SchemaVersion != 1 {
		return errors.New("scheduler baseline must declare schema_version=1")
	}
	if value.RequirementID != "TASK-ORCHESTRATION-OR0" {
		return errors.New("scheduler baseline requirement_id must be TASK-ORCHESTRATION-OR0")
	}
	if len(value.Roots) == 0 {
		return errors.New("scheduler baseline must declare roots")
	}
	for _, root := range value.Roots {
		clean := filepath.Clean(filepath.FromSlash(root))
		if filepath.IsAbs(clean) || clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("scheduler baseline root %q is unsafe", root)
		}
	}
	return nil
}

func scanRoot(repositoryRoot, configuredRoot string, sites map[string]siteLimit) error {
	root := filepath.Join(repositoryRoot, filepath.FromSlash(configuredRoot))
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		measured, err := measureFile(path)
		if err != nil {
			return err
		}
		if measured == (siteLimit{}) {
			return nil
		}
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		sites[filepath.ToSlash(relative)] = measured
		return nil
	})
}

func measureFile(path string) (siteLimit, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return siteLimit{}, err
	}
	timeAliases := make(map[string]bool)
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != "time" {
			continue
		}
		name := "time"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		if name != "_" && name != "." {
			timeAliases[name] = true
		}
	}
	var result siteLimit
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.GoStmt:
			result.GoStatements++
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "Subscribe" {
				result.Subscriptions++
				return true
			}
			packageName, ok := selector.X.(*ast.Ident)
			if !ok || !timeAliases[packageName.Name] {
				return true
			}
			switch selector.Sel.Name {
			case "NewTicker":
				result.NewTickers++
			case "After":
				result.AfterCalls++
			case "AfterFunc":
				result.AfterFuncs++
			}
		}
		return true
	})
	return result, nil
}

func exceeds(value, maximum siteLimit) []string {
	checks := []struct {
		name       string
		value, max int
	}{
		{"go_statements", value.GoStatements, maximum.GoStatements},
		{"new_tickers", value.NewTickers, maximum.NewTickers},
		{"after_calls", value.AfterCalls, maximum.AfterCalls},
		{"after_funcs", value.AfterFuncs, maximum.AfterFuncs},
		{"subscriptions", value.Subscriptions, maximum.Subscriptions},
	}
	var failures []string
	for _, check := range checks {
		if check.value > check.max {
			failures = append(
				failures,
				fmt.Sprintf("%s=%d exceeds %d", check.name, check.value, check.max),
			)
		}
	}
	sort.Strings(failures)
	return failures
}

func add(left, right siteLimit) siteLimit {
	return siteLimit{
		GoStatements:  left.GoStatements + right.GoStatements,
		NewTickers:    left.NewTickers + right.NewTickers,
		AfterCalls:    left.AfterCalls + right.AfterCalls,
		AfterFuncs:    left.AfterFuncs + right.AfterFuncs,
		Subscriptions: left.Subscriptions + right.Subscriptions,
	}
}

func writeReport(path string, value report) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
