package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrustReceiptBindsContentCapabilitiesAndGeneration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plugin.toml")
	if err := os.WriteFile(path, []byte("name = \"fixture\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capabilities := CapabilityInventory{
		FilesystemRoots: []string{"workspace"},
	}
	now := time.Unix(1_700_000_000, 0)
	receipt, err := Review(root, capabilities, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(root, capabilities, 7, receipt); err != nil {
		t.Fatal(err)
	}

	changedCapabilities := CapabilityInventory{
		NetworkHosts: []string{"example.invalid"},
	}
	if err := Verify(root, changedCapabilities, 7, receipt); err == nil ||
		!strings.Contains(err.Error(), "capabilities changed") {
		t.Fatalf("capability change error = %v", err)
	}
	if err := Verify(root, capabilities, 8, receipt); err == nil ||
		!strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("generation change error = %v", err)
	}
	if err := os.WriteFile(path, []byte("name = \"changed\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(root, capabilities, 7, receipt); err == nil ||
		!strings.Contains(err.Error(), "content changed") {
		t.Fatalf("content change error = %v", err)
	}
}

func TestTrustReceiptCapabilityHashIsCanonical(t *testing.T) {
	left, err := HashCapabilities(CapabilityInventory{
		Tools: []string{"write", "read", "read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := HashCapabilities(CapabilityInventory{
		Tools: []string{"read", "write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("canonical hashes differ: %s != %s", left, right)
	}
}

func TestBundleHashIncludesEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	before, err := HashBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := HashBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("adding an empty directory did not change the bundle hash")
	}
}

func TestCapabilityInventoryRejectsWildcards(t *testing.T) {
	if _, err := HashCapabilities(CapabilityInventory{
		NetworkHosts: []string{"*"},
	}); err == nil {
		t.Fatal("wildcard capability was accepted")
	}
}

func TestBundleHashRejectsSymlinksAndHardlinks(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, root string) {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hardlink",
			setup: func(t *testing.T, root string) {
				source := filepath.Join(root, "source")
				if err := os.WriteFile(source, []byte("content"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(source, filepath.Join(root, "linked")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.setup(t, root)
			if _, err := HashBundle(root); err == nil {
				t.Fatal("unsafe bundle was hashed")
			}
		})
	}
}

func TestVerifyFailsClosedWithoutReceipt(t *testing.T) {
	if err := Verify(t.TempDir(), CapabilityInventory{}, 1, Receipt{}); err == nil {
		t.Fatal("empty receipt was accepted")
	}
}
