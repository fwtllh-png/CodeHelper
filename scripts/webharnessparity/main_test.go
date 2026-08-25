package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryHarnessParityContract(t *testing.T) {
	root := filepath.Join("..", "..")
	value, err := readContract(filepath.Join(
		root,
		"testdata/contracts/web-harness-parity.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(value, root); err != nil {
		t.Fatal(err)
	}
}

func TestParityContractRejectsMissingEvidence(t *testing.T) {
	root := t.TempDir()
	value := contract{
		Version: 1, ReferenceCommit: "11bba5f4f11328745f250674d99252c0d23e8398",
		MinimumScore: 95,
		Domains: []domain{
			{ID: "shell_brand_responsive", Points: 12, Checks: []check{
				{ID: "missing", Points: 12, Evidence: []string{"missing.ts"}},
			}},
		},
		Blockers: []check{{ID: "missing", Evidence: []string{"missing.ts"}}},
	}
	if err := validate(value, root); err == nil {
		t.Fatal("missing parity evidence was accepted")
	}
}

func TestEvidenceSelectorMustExist(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "evidence.ts"),
		[]byte("export const present = true\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := validateEvidence(root, []string{"evidence.ts#missing"}); err == nil {
		t.Fatal("missing selector was accepted")
	}
}
