package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryWebExperienceContract(t *testing.T) {
	root := filepath.Join("..", "..")
	value, err := readContract(filepath.Join(
		root,
		"testdata/contracts/web-experience-contract.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(value, root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCSSRejectsUnsafeLiteralsAndMotion(t *testing.T) {
	value := contract{
		CSSPolicy:      cssPolicy{MaxCardRadiusPX: 8},
		SemanticTokens: []string{"--ch-text", "--ch-layer-overlay"},
		StableGeometry: []string{".stable"},
		Motion: []motionRule{{
			ID: "spin", Selector: ".spin", Full: "spin",
			Reduced: "once", Still: "static",
		}},
	}
	base := `
:root {
  --ch-text: #111;
  --ch-layer-overlay: 4;
}
.stable { color: var(--ch-text); }
.spin { animation: spin 1s infinite; z-index: var(--ch-layer-overlay); }
@media (prefers-reduced-motion: reduce) { .spin { animation: none; } }
`
	if err := validateCSS(value, base); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{
		".unsafe { color: #fff; }",
		".unsafe { z-index: 999; }",
		".unsafe { border-radius: 9px; }",
		".unsafe { transition: all 1s; }",
	} {
		if err := validateCSS(value, base+"\n"+unsafe); err == nil {
			t.Fatalf("unsafe CSS was accepted: %s", unsafe)
		}
	}
	if err := validateCSS(
		value,
		strings.ReplaceAll(base, "@media (prefers-reduced-motion: reduce)", "@media print"),
	); err == nil {
		t.Fatal("infinite animation without reduced motion was accepted")
	}
}

func TestReadContractRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contract.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"unknown":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readContract(path); err == nil {
		t.Fatal("unknown contract field was accepted")
	}
}
