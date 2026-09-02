// Command securityeffects enforces the reviewed production side-effect owners.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const policyVersion = 1

type policy struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Rules   []rule `json:"rules"`
}

type rule struct {
	Path       string   `json:"path"`
	Prefix     bool     `json:"prefix,omitempty"`
	Categories []string `json:"categories"`
	Owner      string   `json:"owner"`
	Reason     string   `json:"reason"`
}

type finding struct {
	Path     string
	Category string
	Symbol   string
}

var sensitiveCalls = map[string]map[string]string{
	"github.com/fwtllh-png/QCode/internal/platform/process": {
		"NewCommand":         "process",
		"NewSessionManager":  "process",
		"Run":                "process",
		"StartManaged":       "process",
		"StartStreamManaged": "process",
	},
	"os/exec": {
		"Command": "process", "CommandContext": "process",
	},
	"os": {
		"StartProcess": "process",
		"WriteFile":    "file", "Remove": "file", "RemoveAll": "file",
		"Rename": "file", "Create": "file", "CreateTemp": "file",
		"Mkdir": "file", "MkdirAll": "file", "OpenFile": "file",
	},
	"syscall":               {"Exec": "process"},
	"golang.org/x/sys/unix": {"Exec": "process"},
	"plugin":                {"Open": "process"},
	"net": {
		"Dial": "network", "DialTimeout": "network",
		"Listen": "network", "ListenTCP": "network",
	},
	"net/http": {
		"Get": "network", "Post": "network", "PostForm": "network",
		"ListenAndServe": "network", "Serve": "network",
	},
}

func main() {
	root := flag.String("root", ".", "repository root")
	policyPath := flag.String(
		"policy",
		"testdata/contracts/security-side-effect-entrypoints.json",
		"side-effect ownership policy",
	)
	flag.Parse()
	if err := run(*root, *policyPath, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root, policyPath string, output io.Writer) error {
	configuration, err := loadPolicy(filepath.Join(root, policyPath))
	if err != nil {
		return err
	}
	findings, err := scan(root)
	if err != nil {
		return err
	}
	var violations []string
	matched := make([]bool, len(configuration.Rules))
	for _, item := range findings {
		found := false
		for index, rule := range configuration.Rules {
			if ruleMatches(rule, item) {
				matched[index] = true
				found = true
				break
			}
		}
		if !found {
			violations = append(
				violations,
				fmt.Sprintf("%s: unowned %s side effect %s", item.Path, item.Category, item.Symbol),
			)
		}
	}
	for index, rule := range configuration.Rules {
		if !matched[index] {
			violations = append(violations, fmt.Sprintf(
				"%s: stale %s side-effect ownership rule",
				rule.Path,
				strings.Join(rule.Categories, ","),
			))
		}
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		return errors.New("security side-effect boundary violation:\n- " +
			strings.Join(violations, "\n- "))
	}
	_, _ = fmt.Fprintf(
		output,
		"security side-effect inventory valid: %d call sites, %d ownership rules\n",
		len(findings),
		len(configuration.Rules),
	)
	return nil
}

func loadPolicy(path string) (policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return policy{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result policy
	if err := decoder.Decode(&result); err != nil {
		return policy{}, fmt.Errorf("decode side-effect policy: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return policy{}, errors.New("side-effect policy contains multiple JSON values")
		}
		return policy{}, err
	}
	if result.Version != policyVersion || result.ID != "SEC-EXEC-BOUNDARY-001" {
		return policy{}, errors.New("side-effect policy identity is invalid")
	}
	seen := make(map[string]bool)
	for _, rule := range result.Rules {
		key := rule.Path + "\x00" + strings.Join(rule.Categories, ",")
		if rule.Path == "" || filepath.IsAbs(rule.Path) ||
			strings.TrimSpace(rule.Owner) == "" ||
			strings.TrimSpace(rule.Reason) == "" || seen[key] {
			return policy{}, fmt.Errorf("invalid side-effect ownership rule for %q", rule.Path)
		}
		seen[key] = true
		for _, category := range rule.Categories {
			if category != "process" && category != "file" && category != "network" {
				return policy{}, fmt.Errorf(
					"rule %q has invalid category %q",
					rule.Path,
					category,
				)
			}
		}
	}
	return result, nil
}

func scan(root string) ([]finding, error) {
	var findings []finding
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(
			path string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}
			items, err := scanFile(root, path)
			if err != nil {
				return err
			}
			findings = append(findings, items...)
			return nil
		})
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Symbol < findings[j].Symbol
	})
	return findings, nil
}

func scanFile(root, path string) ([]finding, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		return nil, err
	}
	imports := make(map[string]string)
	for _, spec := range file.Imports {
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		}
		importPath := strings.Trim(spec.Path.Value, `"`)
		if name == "" {
			name = filepath.Base(importPath)
		}
		if _, sensitive := sensitiveCalls[importPath]; sensitive {
			imports[name] = importPath
		}
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	relative = filepath.ToSlash(relative)
	var findings []finding
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok {
			if identifier, identifierOK := selector.X.(*ast.Ident); identifierOK {
				importPath := imports[identifier.Name]
				category := sensitiveCalls[importPath][selector.Sel.Name]
				if category != "" {
					findings = append(findings, finding{
						Path: relative, Category: category,
						Symbol: importPath + "." + selector.Sel.Name,
					})
				}
			}
			return true
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok = call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		methodNetworkCall := selector.Sel.Name == "DialContext" ||
			(selector.Sel.Name == "Do" && len(call.Args) == 1 &&
				looksLikeHTTPRequest(call.Args[0]))
		if methodNetworkCall {
			findings = append(findings, finding{
				Path: relative, Category: "network",
				Symbol: "network-client." + selector.Sel.Name,
			})
			return true
		}
		return true
	})
	return findings, nil
}

func looksLikeHTTPRequest(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}
	switch identifier.Name {
	case "request", "req", "httpRequest":
		return true
	default:
		return false
	}
}

func ruleMatches(candidate rule, item finding) bool {
	if !contains(candidate.Categories, item.Category) {
		return false
	}
	if candidate.Prefix {
		return strings.HasPrefix(item.Path, strings.TrimSuffix(candidate.Path, "/")+"/")
	}
	return item.Path == candidate.Path
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
