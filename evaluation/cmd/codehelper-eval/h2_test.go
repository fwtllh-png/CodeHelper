package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/admission"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func TestH2LiveTaskBindsFrozenIdentityAndPrivateEvidence(t *testing.T) {
	output := t.TempDir()
	lock := freeze.Lock{
		SourceDigest: spec.DigestString("source"),
		LockIdentity: spec.DigestString("lock"),
	}
	task := h2LiveTask(
		"/repository",
		output,
		"h2-test",
		lock,
		admission.H2Scenario{
			ID: "exact-response", Mode: "single",
			Command: []string{"make", "deepseek-live-smoke"},
		},
		3,
		"/repository/bin/codehelper",
	)
	if task.ID != "exact-response-03" ||
		strings.Join(task.Command, " ") != "make deepseek-live-smoke" {
		t.Fatalf("H2 task = %+v", task)
	}
	environment := strings.Join(task.Env, "\n")
	for _, expected := range []string{
		"CODEHELPER_STAGE=h2_live",
		"CODEHELPER_STAGE_RUN_ID=h2-test",
		"CODEHELPER_STAGE_SOURCE_DIGEST=" + lock.SourceDigest,
		"CODEHELPER_STAGE_LOCK_IDENTITY=" + lock.LockIdentity,
		"CODEHELPER_H2_SCENARIO_ID=exact-response",
		"CODEHELPER_H2_SAMPLE_INDEX=3",
		"CODEHELPER_STAGE_EVIDENCE_PATH=" + filepath.Join(
			output,
			"live-evidence",
			"exact-response-03.json",
		),
		"CODEHELPER_LIVE_BINARY=/repository/bin/codehelper",
	} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("H2 environment %q does not contain %q", environment, expected)
		}
	}
}
