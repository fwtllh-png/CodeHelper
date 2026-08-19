package main

import (
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corepack"
)

func TestD1FaultInventoryMatchesExpectedSignatures(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	bundle, err := corepack.Load(
		root,
		"evaluation/scenarios/core/pack.json",
		"evaluation/impact-map.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Pack.FaultCases) != 13 {
		t.Fatalf("D1 Fault Cases = %d, want 13", len(bundle.Pack.FaultCases))
	}
	for _, fault := range bundle.Pack.FaultCases {
		fault := fault
		t.Run(fault.ID, func(t *testing.T) {
			digest, err := checkDiscoveryFault(fault)
			if err != nil {
				t.Fatal(err)
			}
			if digest == "" {
				t.Fatal("D1 Fault Case returned an empty digest")
			}
		})
	}
}
