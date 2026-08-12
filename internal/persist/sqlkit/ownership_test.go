package sqlkit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigratedRepositoriesDoNotReimplementSQLKit(t *testing.T) {
	root := filepath.Clean("../..")
	files := map[string][]string{
		"orchestration/task/repository.go": {
			"func withTx(", "func normalizedObject(", "func normalizedJSON(",
			"func nullable(", "func nullableTime(", "func timestamp(",
		},
		"orchestration/task/execution.go": {
			"withTx(", "normalizedJSON(", "nullable(", "timestamp(",
		},
		"orchestration/task/session.go": {
			"withTx(", "timestamp(",
		},
		"orchestration/automation/repository.go": {
			"func withTx(", "func normalizedObject(", "func nullable(",
			"func nullableTime(", "func timestamp(",
		},
		"orchestration/automation/session.go": {
			"withTx(", "timestamp(",
		},
		"persist/session/lifecycle.go": {
			".BeginTx(",
		},
		"persist/session/profile.go": {
			".BeginTx(", "func normalizedJSON(", "func nullableTime(",
			"func timestamp(",
		},
		"persist/session/repository.go": {
			".BeginTx(", "func normalizedJSON(", "func nullableTime(",
			"func timestamp(",
		},
		"persist/snapshot/repository.go": {
			".BeginTx(", "func normalizedObject(", "func timestamp(",
		},
	}
	for relative, forbidden := range files {
		t.Run(relative, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatal(err)
			}
			source := string(data)
			for _, token := range forbidden {
				if strings.Contains(source, token) {
					t.Fatalf("migrated repository contains forbidden helper %q", token)
				}
			}
		})
	}
}
