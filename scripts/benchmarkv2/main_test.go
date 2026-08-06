package main

import "testing"

func TestRepositoryBenchmarkV2Manifest(t *testing.T) {
	if err := check("../.."); err != nil {
		t.Fatal(err)
	}
}

func TestBenchmarkV2RejectsMissingJourney(t *testing.T) {
	manifest := benchmarkManifest{SchemaVersion: 2}
	_ = manifest
	original := requiredJourneys
	requiredJourneys = []string{"missing"}
	t.Cleanup(func() { requiredJourneys = original })
	if err := check("../.."); err == nil {
		t.Fatal("manifest passed with an unknown required journey")
	}
}
