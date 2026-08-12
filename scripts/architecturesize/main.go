// Command architecturesize enforces net production-line growth over an
// ownership closure. Tests, fixtures, docs, generated sources and build output
// are excluded rather than used to offset growth.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type pathList []string

func (p *pathList) String() string { return strings.Join(*p, ",") }
func (p *pathList) Set(value string) error {
	*p = append(*p, strings.Split(value, ",")...)
	return nil
}

type report struct {
	SchemaVersion int      `json:"schema_version"`
	BaseRef       string   `json:"base_ref"`
	Paths         []string `json:"paths"`
	BaseLines     int      `json:"base_production_lines"`
	HeadLines     int      `json:"head_production_lines"`
	AddedLines    int      `json:"production_lines_added"`
	DeletedLines  int      `json:"production_lines_deleted"`
	NetLines      int      `json:"production_lines_net"`
	MaxNetLines   int      `json:"max_net_lines"`
	Status        string   `json:"status"`
}

func main() {
	var root, base, output string
	var paths pathList
	var limit int
	flag.StringVar(&root, "root", ".", "repository root")
	flag.StringVar(&base, "base-ref", "origin/main", "comparison git ref")
	flag.StringVar(&output, "report", "", "optional JSON report")
	flag.Var(&paths, "paths", "comma-separated ownership paths")
	flag.IntVar(&limit, "max-net", 0, "maximum net growth")
	flag.Parse()
	result, err := measure(root, base, paths, limit)
	if output != "" {
		data, _ := json.MarshalIndent(result, "", "  ")
		var writeErr error
		if writeErr = os.MkdirAll(filepath.Dir(output), 0o755); writeErr == nil {
			writeErr = os.WriteFile(output, append(data, '\n'), 0o644)
		}
		if writeErr != nil {
			err = errors.Join(err, writeErr)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf(
		"architecture size budget passed: base=%d head=%d net=%+d max=%+d\n",
		result.BaseLines, result.HeadLines, result.NetLines, limit,
	)
}

func measure(root, base string, paths []string, limit int) (report, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return report{}, err
	}
	paths = normalizePaths(paths)
	if len(paths) == 0 {
		return report{}, errors.New("ownership closure is empty")
	}
	baseFiles, err := baseSizes(root, base, paths)
	if err != nil {
		return report{}, err
	}
	headFiles, err := headSizes(root, paths)
	if err != nil {
		return report{}, err
	}
	result := summarize(base, paths, baseFiles, headFiles, limit)
	if result.NetLines > limit {
		return result, fmt.Errorf(
			"architecture size budget exceeded: base=%d head=%d net=%+d max=%+d",
			result.BaseLines, result.HeadLines, result.NetLines, limit,
		)
	}
	return result, nil
}

func baseSizes(root, ref string, paths []string) (map[string]int, error) {
	args := append([]string{"ls-tree", "-r", "--name-only", ref, "--"}, paths...)
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read base ref %q: %w", ref, err)
	}
	result := make(map[string]int)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		path := filepath.ToSlash(scanner.Text())
		if !productionPath(path) {
			continue
		}
		command = exec.Command("git", "show", ref+":"+path)
		command.Dir = root
		content, err := command.Output()
		if err != nil {
			return nil, err
		}
		if !generated(content) {
			result[path] = lineCount(content)
		}
	}
	return result, scanner.Err()
}

func headSizes(root string, paths []string) (map[string]int, error) {
	result := make(map[string]int)
	for _, path := range paths {
		err := filepath.WalkDir(
			filepath.Join(root, filepath.FromSlash(path)),
			func(name string, entry fs.DirEntry, err error) error {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				if err != nil {
					return err
				}
				relative, _ := filepath.Rel(root, name)
				relative = filepath.ToSlash(relative)
				if entry.IsDir() {
					if name != root && excludedDirectory(entry.Name()) {
						return filepath.SkipDir
					}
					return nil
				}
				if !productionPath(relative) {
					return nil
				}
				content, err := os.ReadFile(name)
				if err == nil && !generated(content) {
					result[relative] = lineCount(content)
				}
				return err
			},
		)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return result, nil
}

func summarize(
	ref string,
	paths []string,
	base, head map[string]int,
	limit int,
) report {
	result := report{
		SchemaVersion: 1, BaseRef: ref, Paths: paths,
		MaxNetLines: limit, Status: "passed",
	}
	for path, lines := range base {
		result.BaseLines += lines
		if head[path] < lines {
			result.DeletedLines += lines - head[path]
		}
	}
	for path, lines := range head {
		result.HeadLines += lines
		if base[path] < lines {
			result.AddedLines += lines - base[path]
		}
	}
	result.NetLines = result.HeadLines - result.BaseLines
	if result.NetLines > limit {
		result.Status = "failed"
	}
	return result
}

func normalizePaths(paths []string) []string {
	seen := make(map[string]bool)
	result := paths[:0]
	for _, path := range paths {
		path = strings.Trim(filepath.ToSlash(filepath.Clean(path)), "/.")
		if path != "" && !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	sort.Strings(result)
	return result
}

func productionPath(path string) bool {
	base, ext := filepath.Base(path), strings.ToLower(filepath.Ext(path))
	if strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") {
		return false
	}
	if ext != ".go" && ext != ".ts" && ext != ".tsx" &&
		ext != ".js" && ext != ".mjs" && ext != ".cjs" {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if excludedDirectory(part) {
			return false
		}
	}
	return true
}

func excludedDirectory(name string) bool {
	switch name {
	case ".git", ".tmp", "bin", "dist", "docs", "fixtures", "node_modules",
		"testdata", "vendor":
		return true
	}
	return false
}

func generated(content []byte) bool {
	if len(content) > 2048 {
		content = content[:2048]
	}
	return bytes.Contains(content, []byte("Code generated")) &&
		bytes.Contains(content, []byte("DO NOT EDIT"))
}

func lineCount(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	return bytes.Count(content, []byte{'\n'}) +
		boolInt(content[len(content)-1] != '\n')
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
