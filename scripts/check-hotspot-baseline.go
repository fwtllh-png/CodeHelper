// Command check-hotspot-baseline validates the IMP-006 hotspot freeze contract.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type baseline struct {
	SchemaVersion int       `json:"schema_version"`
	RequirementID string    `json:"requirement_id"`
	Hotspots      []hotspot `json:"hotspots"`
}

type hotspot struct {
	ID                     string              `json:"id"`
	Package                string              `json:"package"`
	HotspotFile            string              `json:"hotspot_file"`
	BaselineLines          int                 `json:"baseline_lines"`
	Responsibilities       map[string][]string `json:"responsibilities"`
	ResponsibilityFiles    map[string]string   `json:"responsibility_files,omitempty"`
	AllowedInternalImports []string            `json:"allowed_internal_imports"`
	RequiredTestAssets     []string            `json:"required_test_assets"`
}

func main() {
	var root string
	var path string
	flag.StringVar(&root, "root", ".", "repository root")
	flag.StringVar(&path, "baseline", "docs/hotspot-baseline.json", "baseline JSON path")
	flag.Parse()

	if err := run(root, path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("hotspot baseline check passed")
}

func run(root, path string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	var contract baseline
	if err := json.Unmarshal(data, &contract); err != nil {
		return fmt.Errorf("decode hotspot baseline: %w", err)
	}
	if contract.SchemaVersion != 1 || contract.RequirementID != "IMP-006" {
		return errors.New("hotspot baseline must declare schema_version=1 and requirement_id=IMP-006")
	}
	if len(contract.Hotspots) != 4 {
		return fmt.Errorf("hotspot baseline contains %d hotspots, want 4", len(contract.Hotspots))
	}

	var failures []string
	seen := make(map[string]bool, len(contract.Hotspots))
	for _, item := range contract.Hotspots {
		if item.ID == "" || seen[item.ID] {
			failures = append(failures, fmt.Sprintf("hotspot id %q is empty or duplicated", item.ID))
			continue
		}
		seen[item.ID] = true
		failures = append(failures, checkHotspot(root, item)...)
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		return errors.New("hotspot baseline drift:\n- " + strings.Join(failures, "\n- "))
	}
	return nil
}

func checkHotspot(root string, item hotspot) []string {
	var failures []string
	packageDir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(item.Package, "./")))
	symbols, imports, err := scanPackage(packageDir)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", item.ID, err)}
	}

	for responsibility, required := range item.Responsibilities {
		if responsibility == "" || len(required) == 0 {
			failures = append(failures, fmt.Sprintf("%s: empty responsibility contract", item.ID))
			continue
		}
		for _, symbol := range required {
			files := symbols[symbol]
			if len(files) == 0 {
				failures = append(
					failures,
					fmt.Sprintf("%s: responsibility %s lost symbol %s", item.ID, responsibility, symbol),
				)
				continue
			}
			if owner := item.ResponsibilityFiles[responsibility]; owner != "" &&
				!slices.Contains(files, owner) {
				failures = append(
					failures,
					fmt.Sprintf(
						"%s: responsibility %s symbol %s found in %s, owner is %s",
						item.ID, responsibility, symbol, strings.Join(files, ", "), owner,
					),
				)
			}
		}
	}

	allowed := make(map[string]bool, len(item.AllowedInternalImports))
	for _, path := range item.AllowedInternalImports {
		allowed[path] = true
	}
	for _, path := range imports {
		const prefix = "github.com/fwtllh-png/CodeHelper/"
		if !strings.HasPrefix(path, prefix+"internal/") {
			continue
		}
		relative := strings.TrimPrefix(path, prefix)
		if !allowed[relative] {
			failures = append(
				failures,
				fmt.Sprintf("%s: unreviewed internal dependency %s", item.ID, relative),
			)
		}
	}

	hotspotPath := filepath.Join(root, filepath.FromSlash(item.HotspotFile))
	lines, err := lineCount(hotspotPath)
	if err != nil {
		failures = append(failures, fmt.Sprintf("%s: %v", item.ID, err))
	} else if lines > item.BaselineLines {
		failures = append(
			failures,
			fmt.Sprintf(
				"%s: hotspot grew to %d lines, frozen baseline is %d",
				item.ID, lines, item.BaselineLines,
			),
		)
	}
	for _, asset := range item.RequiredTestAssets {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(asset)))
		if err != nil || info.IsDir() {
			failures = append(failures, fmt.Sprintf("%s: required test asset missing: %s", item.ID, asset))
		}
	}
	return failures
}

func scanPackage(directory string) (map[string][]string, []string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, nil, err
	}
	symbols := make(map[string][]string)
	importSet := make(map[string]bool)
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, err
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				symbols[value.Name.Name] = append(symbols[value.Name.Name], entry.Name())
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok {
						symbols[typeSpec.Name.Name] = append(
							symbols[typeSpec.Name.Name], entry.Name(),
						)
					}
				}
			}
		}
		for _, importSpec := range file.Imports {
			path, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				return nil, nil, err
			}
			importSet[path] = true
		}
	}
	imports := make([]string, 0, len(importSet))
	for path := range importSet {
		imports = append(imports, path)
	}
	sort.Strings(imports)
	return symbols, imports, nil
}

func lineCount(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	lines := 1
	for _, value := range data {
		if value == '\n' {
			lines++
		}
	}
	if data[len(data)-1] == '\n' {
		lines--
	}
	return lines, nil
}
