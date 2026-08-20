package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReportsDeterministicRepositoryInventory(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"check", "--root", root},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("check code=%d stderr=%s", code, stderr.String())
	}
	var summary struct {
		Families        int `json:"families"`
		Cases           int `json:"cases"`
		PairwiseCovered int `json:"pairwise_covered"`
		PairwiseTotal   int `json:"pairwise_total"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Families != 7 ||
		summary.Cases != 105 ||
		summary.PairwiseCovered != 376 ||
		summary.PairwiseCovered != summary.PairwiseTotal {
		t.Fatalf("check summary = %+v", summary)
	}
}

func TestQualifyRequiresImmutableIdentityInputs(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"qualify", "--id", "d2-test"},
		&stdout,
		&stderr,
	)
	if code != 2 ||
		!strings.Contains(stderr.String(), "--base-lock") ||
		!strings.Contains(stderr.String(), "--output") {
		t.Fatalf("qualify code=%d stderr=%s", code, stderr.String())
	}
}

func TestQualifyDriversRequiresFrozenArtifacts(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"qualify-drivers", "--id", "d2-driver-test"},
		&stdout,
		&stderr,
	)
	if code != 2 ||
		!strings.Contains(stderr.String(), "--base-lock") ||
		!strings.Contains(stderr.String(), "--runtime") ||
		!strings.Contains(stderr.String(), "--vsix") ||
		!strings.Contains(stderr.String(), "--output") {
		t.Fatalf("qualify-drivers code=%d stderr=%s", code, stderr.String())
	}
}

func TestCampaignRequiresQualifiedInputsAndArtifacts(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"campaign", "--id", "d2-campaign-test"},
		&stdout,
		&stderr,
	)
	if code != 2 ||
		!strings.Contains(stderr.String(), "--discovery-lock") ||
		!strings.Contains(stderr.String(), "--plan") ||
		!strings.Contains(stderr.String(), "--inventory") ||
		!strings.Contains(stderr.String(), "--runtime") ||
		!strings.Contains(stderr.String(), "--vsix") ||
		!strings.Contains(stderr.String(), "--output") {
		t.Fatalf("campaign code=%d stderr=%s", code, stderr.String())
	}
}

func TestSemanticCampaignRequiresQualifiedRuntime(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"semantic-campaign", "--id", "d2-semantic-test"},
		&stdout,
		&stderr,
	)
	if code != 2 ||
		!strings.Contains(stderr.String(), "--discovery-lock") ||
		!strings.Contains(stderr.String(), "--runtime") ||
		!strings.Contains(stderr.String(), "--output") {
		t.Fatalf(
			"semantic-campaign code=%d stderr=%s",
			code,
			stderr.String(),
		)
	}
}
