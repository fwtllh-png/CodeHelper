package agentcontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDebugMandatoryTruthCapacityEvidence(t *testing.T) {
	debugURL := os.Getenv("DEBUG_SERVER_URL")
	if debugURL == "" {
		t.Skip("DEBUG_SERVER_URL is required for mandatory truth instrumentation")
	}
	report := func(hypothesisID, location, message string, data map[string]any) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"sessionId": "mandatory-truth-capacity", "runId": "post-fix",
			"hypothesisId": hypothesisID, "location": location,
			"msg": "[DEBUG] " + message, "data": data, "ts": time.Now().UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(debugURL, "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	policy := DefaultRetentionPolicy()
	policy.TruthMaxBytes = 1_048_576 - 393_216
	// #region debug-point A:retention-policy
	report("A", "compact_retention_test.go:policy", "default truth retention policy", map[string]any{
		"truthMaxBytes":        policy.TruthMaxBytes,
		"truthMaxEntities":     policy.TruthMaxEntities,
		"mandatoryMaxEntities": policy.MandatoryMaxEntities,
	})
	// #endregion
	current := truthFixture("sha256:compat", "model", 1_048_576)
	large := NewTruthEntity(EntityChange, "large", strings.Repeat("x", 6_000), "runtime.evidence")
	decision := (ContextAdmissionController{Policy: policy}).Decide(
		current,
		AdmissionRequest{AddedMandatory: []TruthEntity{large}},
	)
	// #region debug-point B:admission
	report("B", "compact_retention_test.go:admission", "mandatory write reservation decision", map[string]any{
		"allowed":             decision.Allowed,
		"projectedTruthBytes": decision.ProjectedTruthBytes,
		"reason":              decision.Reason,
		"requiredActions":     decision.RequiredActions,
	})
	// #endregion
	// #region debug-point D:baseline
	report("D", "compact_retention_test.go:baseline", "source capsule baseline", map[string]any{
		"entities": len(current.Entities), "modelContextTokens": 1_048_576,
	})
	// #endregion
}

func TestPlanRetentionKeepsMandatoryAndBoundsOptionalFacts(t *testing.T) {
	capsule := truthFixture("sha256:compat", "model", 8192)
	capsule.Entities = []TruthEntity{
		NewTruthEntity(EntityGoal, "active", "ship bounded context", "runtime.plan"),
		NewTruthEntity(EntityCriticalPath, "critical.go", "critical.go", "runtime.working_set"),
	}
	for index := 0; index < 20; index++ {
		entity := NewTruthEntity(
			EntityFact,
			fmt.Sprintf("fact-%02d", index),
			fmt.Sprintf("fact value %02d", index),
			"runtime.evidence",
		)
		entity.Turn = uint64(index + 1)
		capsule.Entities = append(capsule.Entities, entity)
	}
	capsule.Seal()
	policy := DefaultRetentionPolicy()
	policy.TruthMaxEntities = 6
	policy.MandatoryMaxEntities = 2
	policy.FactMaxEntities = 4
	policy.FailureMaxEntities = 2
	policy.HandleMaxEntities = 2
	policy.OmissionSampleMaxEntities = 2
	policy.TruthMaxBytes = 4096
	retained, receipt, err := PlanRetention(capsule, policy, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained.Entities) != 6 ||
		receipt.MandatoryEntities != 1 ||
		receipt.OmissionCount != 16 {
		t.Fatalf("retained=%+v receipt=%+v", retained.Entities, receipt)
	}
	if retained.Entities[0].Kind != EntityCriticalPath &&
		retained.Entities[1].Kind != EntityCriticalPath {
		t.Fatalf("protected critical path was not retained: %+v", retained.Entities)
	}
	for _, entity := range retained.Entities {
		if entity.Kind == EntityFact && entity.Turn < 17 {
			t.Fatalf("older fact displaced a newer fact: %+v", retained.Entities)
		}
	}
}

func TestPlanRetentionRejectsMandatoryOverflowWithoutDroppingEntity(t *testing.T) {
	capsule := truthFixture("sha256:compat", "model", 8192)
	for index := 0; index < 3; index++ {
		capsule.Entities = append(capsule.Entities, NewTruthEntity(
			EntityTodo,
			fmt.Sprintf("todo-%d", index),
			fmt.Sprintf("todo %d", index),
			"runtime.plan",
		))
	}
	capsule.Seal()
	policy := DefaultRetentionPolicy()
	policy.MandatoryMaxEntities = 2
	policy.TruthMaxEntities = 2
	policy.FactMaxEntities = 1
	policy.FailureMaxEntities = 1
	policy.HandleMaxEntities = 1
	policy.OmissionSampleMaxEntities = 1
	if _, _, err := PlanRetention(capsule, policy, 1); !errors.Is(
		err,
		ErrMandatoryCapacity,
	) {
		t.Fatalf("error=%v, want mandatory capacity", err)
	}
}

func TestPlanRetentionIsDeterministicAcrossEntityOrder(t *testing.T) {
	entities := []TruthEntity{
		NewTruthEntity(EntityGoal, "active", "goal", "runtime.plan"),
		NewTruthEntity(EntityFact, "a", "a", "runtime.evidence"),
		NewTruthEntity(EntityFact, "b", "b", "runtime.evidence"),
		NewTruthEntity(EntityFailure, "failure", "failure", "runtime.failure_ledger"),
	}
	first := testTruthCapsule(1, entities)
	reversed := append([]TruthEntity(nil), entities...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second := testTruthCapsule(1, reversed)
	policy := DefaultRetentionPolicy()
	left, leftReceipt, err := PlanRetention(first, policy, 4)
	if err != nil {
		t.Fatal(err)
	}
	right, rightReceipt, err := PlanRetention(second, policy, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) ||
		!reflect.DeepEqual(leftReceipt, rightReceipt) {
		t.Fatalf("planner changed with input order:\nleft=%+v\nright=%+v", left, right)
	}
}

func TestContextAdmissionRejectsBeforeMandatoryStateExpansion(t *testing.T) {
	current := testTruthCapsule(1, []TruthEntity{
		NewTruthEntity(EntityGoal, "active", "goal", "runtime.plan"),
	})
	controller := ContextAdmissionController{Policy: RetentionPolicy{
		TruthMaxBytes: 4096, TruthMaxEntities: 4,
		MandatoryMaxEntities: 1, FactMaxEntities: 1,
		VerifiedChangeRetentionTurns: 1,
		FailureMaxEntities:           1, HandleMaxEntities: 1,
		OmissionSampleMaxEntities: 1,
	}}
	decision := controller.Decide(current, AdmissionRequest{
		ThreadID: "thread-1",
		AddedMandatory: []TruthEntity{
			NewTruthEntity(EntityTodo, "next", "next", "runtime.plan"),
		},
	})
	if decision.Allowed || decision.ProjectedEntities != 2 ||
		len(decision.RequiredActions) == 0 {
		t.Fatalf("decision=%+v", decision)
	}
	if len(current.Entities) != 1 {
		t.Fatalf("admission mutated current capsule: %+v", current)
	}
}

func TestLifecycleClassificationClearsStaleVerification(t *testing.T) {
	entity := NewTruthEntity(EntityChange, "a.go", "a.go", "runtime.evidence")
	entity.Verified = true
	entity.VerificationSource = "runtime.evidence"
	entity.WorkspacePath = "a.go"
	entity.WorkspaceClaimStatus = WorkspaceClaimStale
	entity.normalizeLifecycle()
	if entity.Verified ||
		entity.Retention != RetentionMandatory ||
		entity.VerificationSource != "" {
		t.Fatalf("stale entity=%+v", entity)
	}
}

func TestRetentionRemainsBoundedAcrossFourHundredEightyTurns(t *testing.T) {
	policy := DefaultRetentionPolicy()
	policy.TruthMaxBytes = 4096
	policy.TruthMaxEntities = 48
	policy.MandatoryMaxEntities = 8
	policy.FactMaxEntities = 32
	policy.FailureMaxEntities = 4
	policy.HandleMaxEntities = 4
	policy.OmissionSampleMaxEntities = 3

	var previous TruthCapsule
	for turn := uint64(1); turn <= 480; turn++ {
		current := truthFixture("sha256:compat", "model", 8192)
		current.Entities = append(current.Entities, NewTruthEntity(
			EntityGoal,
			"active",
			"complete the bounded session",
			"runtime.plan",
		))
		for index := 0; index < 96; index++ {
			entity := NewTruthEntity(
				EntityFact,
				fmt.Sprintf("turn-%03d-fact-%03d", turn, index),
				fmt.Sprintf("fact %03d from turn %03d", index, turn),
				"runtime.evidence",
			)
			entity.Turn = turn
			current.Entities = append(current.Entities, entity)
		}
		current.Seal()
		var (
			merged TruthCapsule
			err    error
		)
		if previous.Generation == 0 {
			merged = current
		} else {
			merged, _, err = MergeTruthCapsules(current, previous)
			if err != nil {
				t.Fatal(err)
			}
		}
		retained, receipt, err := PlanRetention(merged, policy, turn)
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		if len(retained.Entities) > policy.TruthMaxEntities ||
			receipt.TruthBytes > policy.TruthMaxBytes ||
			receipt.MandatoryEntities != 1 {
			t.Fatalf("turn %d retained=%d receipt=%+v",
				turn, len(retained.Entities), receipt)
		}
		for _, omission := range retained.Omissions {
			if len(omission.SampleIDs) > policy.OmissionSampleMaxEntities {
				t.Fatalf("turn %d omission=%+v", turn, omission)
			}
		}
		goalRetained := false
		for _, entity := range retained.Entities {
			goalRetained = goalRetained ||
				entity.Kind == EntityGoal &&
					entity.Value == "complete the bounded session"
		}
		if !goalRetained {
			t.Fatalf("turn %d lost mandatory goal: %+v", turn, retained.Entities)
		}
		previous = retained
	}
}
