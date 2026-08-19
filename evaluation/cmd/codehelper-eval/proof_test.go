package main

import (
	"bytes"
	"context"
	"testing"
)

const discoveryPackage = "github.com/fwtllh-png/CodeHelper/evaluation/internal/discovery"

func TestGoTestProofRequiresAnExecutedPassingTest(t *testing.T) {
	var output bytes.Buffer
	code := runProof(
		context.Background(),
		[]string{
			"go-test", "--minimum", "1", "--",
			"go", "test", "-count=1", discoveryPackage,
			"-run", "^TestD1CatalogRejectsReusedVerification$",
		},
		&output,
		&output,
	)
	if code != 0 {
		t.Fatalf("runProof() = %d: %s", code, output.String())
	}
}

func TestGoTestProofRejectsMissingTest(t *testing.T) {
	var output bytes.Buffer
	code := runProof(
		context.Background(),
		[]string{
			"go-test", "--minimum", "1", "--",
			"go", "test", "-count=1", discoveryPackage,
			"-run", "^TestDoesNotExist$",
		},
		&output,
		&output,
	)
	if code == 0 {
		t.Fatal("runProof accepted a missing test")
	}
}
