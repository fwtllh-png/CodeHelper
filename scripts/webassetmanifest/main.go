package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const manifestName = "asset-manifest.json"

type manifest struct {
	Version int         `json:"version"`
	Files   []assetFile `json:"files"`
}

type assetFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Bytes     int    `json:"bytes"`
	MediaType string `json:"media_type"`
}

func main() {
	dist := flag.String("dist", "web/dist", "Web distribution directory")
	output := flag.String(
		"output",
		"web/dist/"+manifestName,
		"asset manifest output",
	)
	check := flag.Bool("check", false, "fail if the manifest is stale")
	flag.Parse()

	content, err := generate(*dist)
	if err == nil {
		if *check {
			err = checkFile(*output, content)
		} else {
			err = writeFile(*output, content)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Web asset manifest:", err)
		os.Exit(1)
	}
}

func generate(dist string) ([]byte, error) {
	var files []assetFile
	err := filepath.WalkDir(dist, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == manifestName {
			return nil
		}
		relative, err := filepath.Rel(dist, name)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		files = append(files, assetFile{
			Path: filepath.ToSlash(relative), SHA256: hex.EncodeToString(sum[:]),
			Bytes: len(content), MediaType: mediaType(relative),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if len(files) < 4 || !containsPath(files, "index.html") ||
		!containsPath(files, "theme-bootstrap.js") ||
		!containsSuffix(files, ".js") || !containsSuffix(files, ".css") {
		return nil, errors.New("Web distribution is incomplete")
	}
	content, err := json.MarshalIndent(manifest{Version: 1, Files: files}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func mediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func containsPath(files []assetFile, target string) bool {
	for _, file := range files {
		if file.Path == target {
			return true
		}
	}
	return false
}

func containsSuffix(files []assetFile, suffix string) bool {
	for _, file := range files {
		if strings.HasSuffix(file.Path, suffix) {
			return true
		}
	}
	return false
}

func checkFile(path string, expected []byte) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return errors.New(path + " is stale; run make web-build")
	}
	return nil
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
