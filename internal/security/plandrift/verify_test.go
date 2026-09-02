package plandrift

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestVerifyRejectsDrift(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "parser.go")
	original := []byte("package parser\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	document, err := json.Marshal(map[string]any{
		"file_baseline": []map[string]any{
			{"path": "parser.go", "digest": hex.EncodeToString(sum[:])},
			{"path": "new.go", "missing": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(root, document); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(root, document); protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("drift error = %v", err)
	}
}
