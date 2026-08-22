package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateProducesDeterministicGuardedRoutes(t *testing.T) {
	first, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	if string(first.schema) != string(second.schema) ||
		string(first.typeScript) != string(second.typeScript) {
		t.Fatal("generated Web Host contract is not deterministic")
	}
	var contract struct {
		ProtocolVersion int  `json:"protocol_version"`
		LoopbackOnly    bool `json:"loopback_only"`
		SameOriginOnly  bool `json:"same_origin_only"`
		Routes          []struct {
			Path               string `json:"path"`
			Mutation           bool   `json:"mutation"`
			IdempotencyKey     bool   `json:"idempotency_key"`
			RequiresCapability bool   `json:"requires_capability"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(first.schema, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.ProtocolVersion != 1 || !contract.LoopbackOnly || !contract.SameOriginOnly {
		t.Fatalf("transport guard = %+v", contract)
	}
	seenSubmit := false
	for _, route := range contract.Routes {
		if route.Path == "/api/v1/operation/submit" {
			seenSubmit = route.Mutation && route.IdempotencyKey &&
				route.RequiresCapability
		}
	}
	if !seenSubmit {
		t.Fatal("operation submit is not represented as a guarded idempotent mutation")
	}
	if !strings.Contains(
		string(first.typeScript),
		`export type WebRPCRoute = (typeof webRPCRoutes)[number]`,
	) {
		t.Fatal("generated TypeScript route union is missing")
	}
}

func TestCheckFileRejectsDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-host.schema.json")
	if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkFile(path, []byte("current\n")); err == nil {
		t.Fatal("stale schema was accepted")
	}
}
