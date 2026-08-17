// Command architecturemetrics measures and enforces repository architecture budgets.
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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const modulePrefix = "github.com/fwtllh-png/CodeHelper/"

var metricHeadroom = map[string]int{
	"internal_fanout":    0,
	"production_lines":   100,
	"options_fields":     0,
	"mutex_fields":       0,
	"lines":              20,
	"max_function_lines": 5,
	"event_switch_sites": 0,
}

type baseline struct {
	SchemaVersion int               `json:"schema_version"`
	RequirementID string            `json:"requirement_id"`
	Targets       []target          `json:"targets"`
	Retirements   map[string]string `json:"retirements,omitempty"`
}

type target struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Path        string            `json:"path"`
	Limits      map[string]int    `json:"limits"`
	Relaxations map[string]string `json:"relaxations,omitempty"`
}

type report struct {
	SchemaVersion int              `json:"schema_version"`
	RequirementID string           `json:"requirement_id"`
	Targets       []measuredTarget `json:"targets"`
}

type measuredTarget struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Path    string         `json:"path"`
	Metrics map[string]int `json:"metrics"`
}

func main() {
	var root string
	var baselinePath string
	var reportPath string
	var baseRef string
	var baseBaselinePath string
	flag.StringVar(&root, "root", ".", "repository root")
	flag.StringVar(
		&baselinePath,
		"baseline",
		"testdata/contracts/architecture-metrics-baseline.json",
		"architecture metrics baseline",
	)
	flag.StringVar(&reportPath, "report", "", "optional measured JSON report path")
	flag.StringVar(&baseRef, "base-ref", "", "optional git ref used to enforce monotonic limits")
	flag.StringVar(
		&baseBaselinePath,
		"base-baseline",
		"",
		"optional baseline path at base-ref",
	)
	flag.Parse()

	result, err := runWithBaseBaseline(
		root,
		baselinePath,
		reportPath,
		baseRef,
		baseBaselinePath,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("architecture metrics passed for %d targets\n", len(result.Targets))
}

func run(root, baselinePath, reportPath, baseRef string) (report, error) {
	return runWithBaseBaseline(root, baselinePath, reportPath, baseRef, "")
}

func runWithBaseBaseline(
	root,
	baselinePath,
	reportPath,
	baseRef,
	baseBaselinePath string,
) (report, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return report{}, err
	}
	contract, err := readBaseline(filepath.Join(root, filepath.FromSlash(baselinePath)))
	if err != nil {
		return report{}, err
	}
	if err := validateBaseline(contract); err != nil {
		return report{}, err
	}
	if strings.TrimSpace(baseRef) != "" {
		if strings.TrimSpace(baseBaselinePath) == "" {
			baseBaselinePath = baselinePath
		}
		previous, err := baselineAtRef(root, baseRef, baseBaselinePath)
		if err != nil {
			return report{}, err
		}
		if err := validateRatchet(previous, contract); err != nil {
			return report{}, err
		}
	}

	result := report{
		SchemaVersion: 1,
		RequirementID: contract.RequirementID,
		Targets:       make([]measuredTarget, 0, len(contract.Targets)),
	}
	var failures []string
	for _, item := range contract.Targets {
		metrics, err := measure(root, item)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", item.ID, err))
			continue
		}
		result.Targets = append(result.Targets, measuredTarget{
			ID: item.ID, Kind: item.Kind, Path: item.Path, Metrics: metrics,
		})
		for name, maximum := range item.Limits {
			value, exists := metrics[name]
			if !exists {
				failures = append(
					failures,
					fmt.Sprintf("%s: unsupported metric %q for %s target", item.ID, name, item.Kind),
				)
				continue
			}
			if value > maximum {
				failures = append(
					failures,
					fmt.Sprintf("%s: %s is %d, maximum is %d", item.ID, name, value, maximum),
				)
				continue
			}
			headroom, exists := metricHeadroom[name]
			if exists && maximum-value > headroom {
				failures = append(
					failures,
					fmt.Sprintf(
						"%s: %s limit is stale at %d for measured %d; maximum headroom is %d",
						item.ID, name, maximum, value, headroom,
					),
				)
			}
		}
	}
	sort.Slice(result.Targets, func(left, right int) bool {
		return result.Targets[left].ID < result.Targets[right].ID
	})
	if reportPath != "" {
		if err := writeReport(root, reportPath, result); err != nil {
			return report{}, err
		}
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		return result, errors.New(
			"architecture metrics drift:\n- " + strings.Join(failures, "\n- "),
		)
	}
	return result, nil
}

func readBaseline(path string) (baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return baseline{}, err
	}
	var result baseline
	if err := json.Unmarshal(data, &result); err != nil {
		return baseline{}, fmt.Errorf("decode architecture baseline: %w", err)
	}
	return result, nil
}

func validateBaseline(contract baseline) error {
	if contract.SchemaVersion != 1 {
		return errors.New("architecture baseline must declare schema_version=1")
	}
	if contract.RequirementID != "ARCH-RATCHET-001" {
		return errors.New("architecture baseline must declare requirement_id=ARCH-RATCHET-001")
	}
	if len(contract.Targets) == 0 {
		return errors.New("architecture baseline has no targets")
	}
	seen := make(map[string]bool, len(contract.Targets))
	for _, item := range contract.Targets {
		if strings.TrimSpace(item.ID) == "" || seen[item.ID] {
			return fmt.Errorf("architecture target id %q is empty or duplicated", item.ID)
		}
		seen[item.ID] = true
		switch item.Kind {
		case "package", "file", "repository":
		default:
			return fmt.Errorf("architecture target %s has unsupported kind %q", item.ID, item.Kind)
		}
		if strings.TrimSpace(item.Path) == "" {
			return fmt.Errorf("architecture target %s has no path", item.ID)
		}
		cleanPath := filepath.Clean(filepath.FromSlash(item.Path))
		if filepath.IsAbs(cleanPath) || cleanPath == ".." ||
			strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
			return fmt.Errorf("architecture target %s has unsafe path %q", item.ID, item.Path)
		}
		if len(item.Limits) == 0 {
			return fmt.Errorf("architecture target %s has no limits", item.ID)
		}
		for name, value := range item.Limits {
			if strings.TrimSpace(name) == "" || value < 0 {
				return fmt.Errorf("architecture target %s has invalid limit %q=%d", item.ID, name, value)
			}
		}
		for name, reason := range item.Relaxations {
			if _, exists := item.Limits[name]; !exists {
				return fmt.Errorf(
					"architecture target %s relaxes unknown metric %q",
					item.ID, name,
				)
			}
			if strings.TrimSpace(reason) == "" {
				return fmt.Errorf(
					"architecture target %s relaxation %q has no reason",
					item.ID, name,
				)
			}
		}
	}
	for key, reason := range contract.Retirements {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(reason) == "" {
			return fmt.Errorf("architecture retirement %q must include a reason", key)
		}
	}
	return nil
}

func validateRatchet(previous, current baseline) error {
	if err := validateBaseline(previous); err != nil {
		return fmt.Errorf("base architecture baseline: %w", err)
	}
	oldTargets := make(map[string]target, len(previous.Targets))
	for _, item := range previous.Targets {
		oldTargets[item.ID] = item
	}
	currentTargets := make(map[string]target, len(current.Targets))
	for _, item := range current.Targets {
		currentTargets[item.ID] = item
	}
	var failures []string
	for _, item := range current.Targets {
		old, exists := oldTargets[item.ID]
		if !exists {
			continue
		}
		if item.Kind != old.Kind || item.Path != old.Path {
			failures = append(
				failures,
				fmt.Sprintf(
					"%s: target identity changed from %s:%s to %s:%s",
					item.ID, old.Kind, old.Path, item.Kind, item.Path,
				),
			)
		}
		for metric, maximum := range item.Limits {
			oldMaximum, exists := old.Limits[metric]
			if !exists || maximum <= oldMaximum {
				if reason := strings.TrimSpace(item.Relaxations[metric]); reason != "" {
					failures = append(
						failures,
						fmt.Sprintf("%s: stale relaxation for %s", item.ID, metric),
					)
				}
				continue
			}
			if strings.TrimSpace(item.Relaxations[metric]) == "" {
				failures = append(
					failures,
					fmt.Sprintf(
						"%s: %s limit increased from %d to %d without an explicit relaxation",
						item.ID, metric, oldMaximum, maximum,
					),
				)
			}
		}
	}
	requiredRetirements := make(map[string]bool)
	for _, old := range previous.Targets {
		item, exists := currentTargets[old.ID]
		if !exists {
			requiredRetirements[old.ID] = true
			if strings.TrimSpace(current.Retirements[old.ID]) == "" {
				failures = append(
					failures,
					fmt.Sprintf("%s: target removed without an explicit retirement", old.ID),
				)
			}
			continue
		}
		for metric := range old.Limits {
			if _, exists := item.Limits[metric]; exists {
				continue
			}
			key := old.ID + "." + metric
			requiredRetirements[key] = true
			if strings.TrimSpace(current.Retirements[key]) == "" {
				failures = append(
					failures,
					fmt.Sprintf("%s: metric removed without an explicit retirement", key),
				)
			}
		}
	}
	for key := range current.Retirements {
		if !requiredRetirements[key] {
			failures = append(failures, fmt.Sprintf("%s: stale architecture retirement", key))
		}
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		return errors.New("architecture ratchet violation:\n- " + strings.Join(failures, "\n- "))
	}
	return nil
}

func baselineAtRef(root, ref, baselinePath string) (baseline, error) {
	relative := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(baselinePath), "./"))
	command := exec.Command("git", "show", ref+":"+relative)
	command.Dir = root
	data, err := command.Output()
	if err != nil {
		return baseline{}, fmt.Errorf("read architecture baseline at %s: %w", ref, err)
	}
	var result baseline
	if err := json.Unmarshal(data, &result); err != nil {
		return baseline{}, fmt.Errorf("decode architecture baseline at %s: %w", ref, err)
	}
	return result, nil
}

func measure(root string, item target) (map[string]int, error) {
	switch item.Kind {
	case "package":
		return measurePackage(filepath.Join(root, filepath.FromSlash(item.Path)))
	case "file":
		return measureFile(filepath.Join(root, filepath.FromSlash(item.Path)))
	case "repository":
		return measureRepository(filepath.Join(root, filepath.FromSlash(item.Path)))
	default:
		return nil, fmt.Errorf("unsupported target kind %q", item.Kind)
	}
}

func measurePackage(directory string) (map[string]int, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	files := token.NewFileSet()
	imports := make(map[string]bool)
	productionLines := 0
	optionsFields := 0
	mutexFields := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		lines, err := lineCount(path)
		if err != nil {
			return nil, err
		}
		productionLines += lines
		file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, imported := range file.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(value, modulePrefix+"internal/") {
				imports[value] = true
			}
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, spec := range general.Specs {
				typed, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structure, ok := typed.Type.(*ast.StructType)
				if !ok {
					continue
				}
				if strings.HasSuffix(typed.Name.Name, "Options") {
					optionsFields += fieldCount(structure.Fields.List)
				}
				mutexFields += countMutexFields(structure.Fields.List)
			}
		}
	}
	return map[string]int{
		"internal_fanout":  len(imports),
		"production_lines": productionLines,
		"options_fields":   optionsFields,
		"mutex_fields":     mutexFields,
	}, nil
}

func measureFile(path string) (map[string]int, error) {
	lines, err := lineCount(path)
	if err != nil {
		return nil, err
	}
	result := map[string]int{"lines": lines}
	if filepath.Ext(path) != ".go" {
		return result, nil
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	maximum := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		start := files.Position(function.Pos()).Line
		end := files.Position(function.End()).Line
		maximum = max(maximum, end-start+1)
	}
	result["max_function_lines"] = maximum
	return result, nil
}

func measureRepository(root string) (map[string]int, error) {
	sites := 0
	for _, relative := range []string{"internal", "extensions/vscode/src"} {
		directory := filepath.Join(root, filepath.FromSlash(relative))
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "node_modules" || entry.Name() == "dist" {
					return filepath.SkipDir
				}
				return nil
			}
			name := entry.Name()
			switch filepath.Ext(name) {
			case ".go":
				if strings.HasSuffix(name, "_test.go") {
					return nil
				}
				count, err := goEventSwitches(path)
				if err != nil {
					return err
				}
				sites += count
			case ".ts":
				if strings.HasSuffix(name, ".test.ts") || name == "generated.ts" {
					return nil
				}
				count, err := typescriptEventSwitches(path)
				if err != nil {
					return err
				}
				sites += count
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return map[string]int{"event_switch_sites": sites}, nil
}

func goEventSwitches(path string) (int, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return 0, err
	}
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		statement, ok := node.(*ast.SwitchStmt)
		if !ok || statement.Body == nil {
			return true
		}
		found := false
		ast.Inspect(statement.Body, func(child ast.Node) bool {
			selector, ok := child.(*ast.SelectorExpr)
			if ok && strings.HasPrefix(selector.Sel.Name, "Event") {
				found = true
			}
			return !found
		})
		if found {
			count++
		}
		return true
	})
	return count, nil
}

func typescriptEventSwitches(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for offset := 0; offset < len(data); {
		index := bytes.Index(data[offset:], []byte("switch ("))
		if index < 0 {
			break
		}
		start := offset + index
		open := bytes.IndexByte(data[start:], '{')
		if open < 0 {
			break
		}
		open += start
		close := matchingBrace(data, open)
		if close < 0 {
			break
		}
		block := data[open:close]
		if bytes.Contains(block, []byte(`case "turn.`)) ||
			bytes.Contains(block, []byte(`case "tool.`)) ||
			bytes.Contains(block, []byte(`case "approval.`)) ||
			bytes.Contains(block, []byte(`case "input.`)) ||
			bytes.Contains(block, []byte(`case "output.`)) {
			count++
		}
		offset = close + 1
	}
	return count, nil
}

func matchingBrace(data []byte, open int) int {
	depth := 0
	var quote byte
	escaped := false
	for index := open; index < len(data); index++ {
		value := data[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if value == '\\' {
				escaped = true
				continue
			}
			if value == quote {
				quote = 0
			}
			continue
		}
		switch value {
		case '\'', '"', '`':
			quote = value
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func fieldCount(fields []*ast.Field) int {
	count := 0
	for _, field := range fields {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

func countMutexFields(fields []*ast.Field) int {
	count := 0
	for _, field := range fields {
		selector, ok := field.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok && identifier.Name == "sync" &&
			(selector.Sel.Name == "Mutex" || selector.Sel.Name == "RWMutex") {
			count += max(1, len(field.Names))
		}
	}
	return count
}

func lineCount(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	lines := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines, nil
}

func writeReport(root, path string, value report) error {
	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, filepath.FromSlash(path))
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return err
	}
	return nil
}
