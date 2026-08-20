package d2

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

var requiredDriverIDs = []string{"acp", "cli", "vscode"}

var requiredFaultIDs = []string{
	"filesystem_pressure",
	"mcp_disconnect",
	"persistence_contention",
	"process_crash",
	"provider_disconnect",
	"tool_timeout",
}

var requiredOwnedResources = []string{
	"durable_state",
	"lock",
	"port",
	"process",
	"subscription",
	"temporary_path",
}

type DriverInventory struct {
	SchemaVersion  int                `json:"schema_version"`
	CampaignID     string             `json:"campaign_id"`
	PlanDigest     string             `json:"plan_digest"`
	Drivers        []DriverDefinition `json:"drivers"`
	Faults         []FaultDefinition  `json:"faults"`
	Cases          []GeneratedCase    `json:"cases"`
	EvidenceDigest string             `json:"evidence_digest"`
}

type DriverDefinition struct {
	ID              string `json:"id"`
	EntryPoint      string `json:"entry_point"`
	ProductionPath  string `json:"production_path"`
	NegativeControl string `json:"negative_control"`
}

type FaultDefinition struct {
	ID       string `json:"id"`
	Boundary string `json:"boundary"`
	Mode     string `json:"mode"`
}

type GeneratedCase struct {
	ID             string            `json:"id"`
	FamilyID       string            `json:"family_id"`
	Seed           uint64            `json:"seed"`
	DriverID       string            `json:"driver_id"`
	Values         map[string]string `json:"values"`
	Workload       WorkloadInput     `json:"workload"`
	Steps          []JourneyStep     `json:"steps"`
	Schedule       []ScheduleAction  `json:"schedule"`
	Faults         []FaultControl    `json:"faults"`
	Assertions     []string          `json:"assertions"`
	OwnedResources []OwnedResource   `json:"owned_resources"`
	Cleanup        []CleanupAction   `json:"cleanup"`
	EvidenceDigest string            `json:"evidence_digest"`
}

type WorkloadInput struct {
	Files          int `json:"files"`
	ContextBytes   int `json:"context_bytes"`
	OutputBytes    int `json:"output_bytes"`
	DurableRecords int `json:"durable_records"`
}

type JourneyStep struct {
	Sequence int    `json:"sequence"`
	Action   string `json:"action"`
	Boundary string `json:"boundary"`
}

type ScheduleAction struct {
	Tick   int    `json:"tick"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
}

type FaultControl struct {
	ID          string `json:"id"`
	TriggerStep int    `json:"trigger_step"`
	ExpectedHit int    `json:"expected_hit"`
}

type OwnedResource struct {
	Kind  string `json:"kind"`
	Owner string `json:"owner"`
	ID    string `json:"id"`
}

type CleanupAction struct {
	Sequence int    `json:"sequence"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
}

func BuildDriverInventory(campaign Campaign, plan Plan) (DriverInventory, error) {
	if err := campaign.Validate(); err != nil {
		return DriverInventory{}, err
	}
	if plan.CampaignID != campaign.ID ||
		plan.EvidenceDigest != digestPlan(plan) {
		return DriverInventory{}, errors.New("D2 Driver plan identity is invalid")
	}
	inventory := DriverInventory{
		SchemaVersion: SchemaVersion,
		CampaignID:    campaign.ID,
		PlanDigest:    plan.EvidenceDigest,
		Drivers: []DriverDefinition{
			{
				ID: "acp", EntryPoint: "acp",
				ProductionPath:  "codehelper host --adapter acp",
				NegativeControl: "reject_unsupported_protocol",
			},
			{
				ID: "cli", EntryPoint: "cli",
				ProductionPath:  "codehelper exec",
				NegativeControl: "reject_invalid_output_format",
			},
			{
				ID: "vscode", EntryPoint: "vscode",
				ProductionPath:  "official_extension_runtime_client",
				NegativeControl: "deny_untrusted_write",
			},
		},
		Faults: []FaultDefinition{
			{ID: "filesystem_pressure", Boundary: "filesystem", Mode: "bounded_permission_denial"},
			{ID: "mcp_disconnect", Boundary: "mcp", Mode: "owned_stdio_exit"},
			{ID: "persistence_contention", Boundary: "persistence", Mode: "bounded_exclusive_lock"},
			{ID: "process_crash", Boundary: "process", Mode: "owned_process_termination"},
			{ID: "provider_disconnect", Boundary: "provider", Mode: "fixture_stream_disconnect"},
			{ID: "tool_timeout", Boundary: "guarded_tool", Mode: "bounded_command_timeout"},
		},
	}
	for _, planned := range plan.Cases {
		generated := generateCase(planned)
		generated.EvidenceDigest = digestGeneratedCase(generated)
		inventory.Cases = append(inventory.Cases, generated)
	}
	inventory.EvidenceDigest = digestDriverInventory(inventory)
	return inventory, inventory.Validate()
}

func (i DriverInventory) Validate() error {
	if i.SchemaVersion != SchemaVersion || !validID(i.CampaignID) ||
		!validDigest(i.PlanDigest) || len(i.Cases) == 0 {
		return errors.New("D2 Driver inventory identity is invalid")
	}
	driverIDs := make([]string, 0, len(i.Drivers))
	for _, driver := range i.Drivers {
		if !validID(driver.ID) || driver.EntryPoint != driver.ID ||
			strings.TrimSpace(driver.ProductionPath) == "" ||
			!validID(driver.NegativeControl) {
			return fmt.Errorf("D2 Driver %q is invalid", driver.ID)
		}
		driverIDs = append(driverIDs, driver.ID)
	}
	slices.Sort(driverIDs)
	if !slices.Equal(driverIDs, requiredDriverIDs) {
		return errors.New("D2 Driver catalog is incomplete")
	}
	faultIDs := make([]string, 0, len(i.Faults))
	for _, fault := range i.Faults {
		if !validID(fault.ID) || !validID(fault.Boundary) ||
			!validID(fault.Mode) {
			return fmt.Errorf("D2 fault %q is invalid", fault.ID)
		}
		faultIDs = append(faultIDs, fault.ID)
	}
	slices.Sort(faultIDs)
	if !slices.Equal(faultIDs, requiredFaultIDs) {
		return errors.New("D2 fault catalog is incomplete")
	}
	seenCases := make(map[string]struct{}, len(i.Cases))
	driverHits := make(map[string]int)
	faultHits := make(map[string]int)
	for _, generated := range i.Cases {
		if err := generated.Validate(); err != nil {
			return err
		}
		if _, duplicate := seenCases[generated.ID]; duplicate {
			return fmt.Errorf("duplicate D2 generated Case %q", generated.ID)
		}
		seenCases[generated.ID] = struct{}{}
		driverHits[generated.DriverID]++
		for _, fault := range generated.Faults {
			faultHits[fault.ID] += fault.ExpectedHit
		}
	}
	for _, id := range requiredDriverIDs {
		if driverHits[id] == 0 {
			return fmt.Errorf("D2 Driver %q has no generated Case", id)
		}
	}
	for _, id := range requiredFaultIDs {
		if faultHits[id] == 0 {
			return fmt.Errorf("D2 fault %q has no trigger", id)
		}
	}
	if i.EvidenceDigest != digestDriverInventory(i) {
		return errors.New("D2 Driver inventory digest is invalid")
	}
	return nil
}

func (c GeneratedCase) Validate() error {
	if !validID(c.ID) || !validID(c.FamilyID) ||
		!slices.Contains(requiredDriverIDs, c.DriverID) || c.Seed == 0 ||
		len(c.Values) < 2 || len(c.Steps) < 4 || len(c.Schedule) == 0 ||
		len(c.Assertions) == 0 {
		return fmt.Errorf("D2 generated Case %q is incomplete", c.ID)
	}
	if c.Workload.Files < 1 || c.Workload.Files > 2_000 ||
		c.Workload.ContextBytes < 1 || c.Workload.ContextBytes > 4<<20 ||
		c.Workload.OutputBytes < 1 || c.Workload.OutputBytes > 1<<20 ||
		c.Workload.DurableRecords < 1 || c.Workload.DurableRecords > 10_000 {
		return fmt.Errorf("D2 generated Case %q workload is unbounded", c.ID)
	}
	for index, step := range c.Steps {
		if step.Sequence != index+1 || !validID(step.Action) ||
			!validID(step.Boundary) {
			return fmt.Errorf("D2 generated Case %q has an invalid Journey", c.ID)
		}
	}
	lastTick := -1
	for _, action := range c.Schedule {
		if action.Tick < lastTick || !validID(action.Actor) ||
			!validID(action.Action) {
			return fmt.Errorf("D2 generated Case %q has an invalid schedule", c.ID)
		}
		lastTick = action.Tick
	}
	for _, fault := range c.Faults {
		if !slices.Contains(requiredFaultIDs, fault.ID) ||
			fault.TriggerStep < 1 || fault.TriggerStep > len(c.Steps) ||
			fault.ExpectedHit != 1 {
			return fmt.Errorf("D2 generated Case %q has an invalid fault", c.ID)
		}
	}
	kinds := make([]string, 0, len(c.OwnedResources))
	for _, resource := range c.OwnedResources {
		if !validID(resource.Kind) || resource.Owner != c.ID ||
			!validID(resource.ID) {
			return fmt.Errorf("D2 generated Case %q has invalid ownership", c.ID)
		}
		kinds = append(kinds, resource.Kind)
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, requiredOwnedResources) ||
		len(c.Cleanup) != len(c.OwnedResources) {
		return fmt.Errorf("D2 generated Case %q cleanup is incomplete", c.ID)
	}
	for index, cleanup := range c.Cleanup {
		resource := c.OwnedResources[len(c.OwnedResources)-1-index]
		if cleanup.Sequence != index+1 || cleanup.Kind != resource.Kind ||
			cleanup.ID != resource.ID {
			return fmt.Errorf("D2 generated Case %q cleanup is not exact", c.ID)
		}
	}
	if c.EvidenceDigest != digestGeneratedCase(c) {
		return fmt.Errorf("D2 generated Case %q digest is invalid", c.ID)
	}
	return nil
}

func generateCase(planned PlannedCase) GeneratedCase {
	driverID := driverFor(planned)
	generated := GeneratedCase{
		ID:       planned.ID,
		FamilyID: planned.FamilyID,
		Seed:     planned.Seed,
		DriverID: driverID,
		Values:   maps.Clone(planned.Values),
		Workload: workloadFor(planned.Values["workload"]),
		Assertions: []string{
			"exactly_one_terminal",
			"identity_preserved",
			"no_private_content",
			"resource_closure",
		},
	}
	actions := []string{"prepare_workspace", "start_runtime", "submit_prompt"}
	switch planned.Values["session_state"] {
	case "long_compacted":
		actions = append(actions, "extend_session", "observe_compaction")
	case "checkpoint_resume":
		actions = append(actions, "list_checkpoint", "restore_checkpoint", "resume_session")
	case "canceled_effect":
		actions = append(actions, "start_effect", "cancel_turn")
	}
	switch planned.Values["lifecycle"] {
	case "crash_recovery":
		actions = append(actions, "crash_runtime", "restart_runtime", "reconnect_session")
	case "version_upgrade":
		actions = append(actions, "stop_runtime", "upgrade_runtime", "restart_runtime")
	case "rollback_reconnect":
		actions = append(actions, "stop_runtime", "rollback_runtime", "reconnect_session")
	}
	actions = append(actions, "observe_terminal")
	for index, action := range actions {
		generated.Steps = append(generated.Steps, JourneyStep{
			Sequence: index + 1,
			Action:   action,
			Boundary: boundaryFor(action, driverID),
		})
	}
	generated.Schedule = scheduleFor(planned, actions)
	generated.Faults = faultsFor(planned, len(actions))
	if planned.FamilyID == "differential_host" {
		generated.Assertions = append(
			generated.Assertions,
			"equivalent_task_terminal",
			"equivalent_task_receipts",
		)
	}
	if planned.Values["lifecycle"] != "" &&
		planned.Values["lifecycle"] != "clean_start" {
		generated.Assertions = append(
			generated.Assertions,
			"restart_boundary_replay",
		)
	}
	for _, kind := range requiredOwnedResources {
		generated.OwnedResources = append(generated.OwnedResources, OwnedResource{
			Kind: kind, Owner: planned.ID, ID: planned.ID + "-" + kind,
		})
	}
	for index := len(generated.OwnedResources) - 1; index >= 0; index-- {
		resource := generated.OwnedResources[index]
		generated.Cleanup = append(generated.Cleanup, CleanupAction{
			Sequence: len(generated.Cleanup) + 1,
			Kind:     resource.Kind,
			ID:       resource.ID,
		})
	}
	return generated
}

func driverFor(planned PlannedCase) string {
	if planned.FamilyID == "live_model_variability" &&
		planned.Values["model_variability"] == "live_primary" {
		return "cli"
	}
	switch planned.Values["topology"] {
	case "cli":
		return "cli"
	case "vscode":
		return "vscode"
	default:
		return "acp"
	}
}

func workloadFor(value string) WorkloadInput {
	switch value {
	case "polyglot_large":
		return WorkloadInput{Files: 1200, ContextBytes: 3 << 20, OutputBytes: 512 << 10, DurableRecords: 8000}
	case "dirty_multifile":
		return WorkloadInput{Files: 80, ContextBytes: 512 << 10, OutputBytes: 128 << 10, DurableRecords: 1200}
	default:
		return WorkloadInput{Files: 8, ContextBytes: 32 << 10, OutputBytes: 16 << 10, DurableRecords: 64}
	}
}

func boundaryFor(action, driverID string) string {
	switch action {
	case "prepare_workspace":
		return "filesystem"
	case "start_runtime", "stop_runtime", "crash_runtime", "restart_runtime",
		"upgrade_runtime", "rollback_runtime":
		return "process"
	case "list_checkpoint", "restore_checkpoint", "resume_session",
		"extend_session", "observe_compaction":
		return "persistence"
	case "start_effect", "cancel_turn":
		return "runtime"
	default:
		return driverID
	}
}

func scheduleFor(planned PlannedCase, actions []string) []ScheduleAction {
	actors := []string{"primary"}
	if planned.FamilyID == "concurrency_cancellation" {
		actors = []string{"primary", "secondary", "controller"}
	}
	schedule := make([]ScheduleAction, 0, len(actions))
	tick := int(planned.Seed % 7)
	for index, action := range actions {
		tick += 1 + int((planned.Seed+uint64(index))%3)
		schedule = append(schedule, ScheduleAction{
			Tick: tick, Actor: actors[index%len(actors)], Action: action,
		})
	}
	return schedule
}

func faultsFor(planned PlannedCase, steps int) []FaultControl {
	var ids []string
	switch planned.Values["dependency_behavior"] {
	case "provider_disconnect":
		ids = append(ids, "provider_disconnect")
	case "sqlite_contention":
		ids = append(ids, "persistence_contention")
	case "filesystem_pressure":
		ids = append(ids, "filesystem_pressure")
	}
	if planned.Values["lifecycle"] == "crash_recovery" {
		ids = append(ids, "process_crash")
	}
	if planned.FamilyID == "composed_faults" {
		parity := 0
		for _, value := range planned.ID {
			parity += int(value)
		}
		if parity%2 == 0 {
			ids = append(ids, "mcp_disconnect")
		} else {
			ids = append(ids, "tool_timeout")
		}
	}
	if planned.Values["session_state"] == "canceled_effect" {
		ids = append(ids, "tool_timeout")
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	faults := make([]FaultControl, 0, len(ids))
	for index, id := range ids {
		faults = append(faults, FaultControl{
			ID: id, TriggerStep: 2 + index%(steps-1), ExpectedHit: 1,
		})
	}
	return faults
}

func digestGeneratedCase(value GeneratedCase) string {
	value.EvidenceDigest = ""
	raw, _ := json.Marshal(value)
	return spec.DigestString(string(raw))
}

func digestDriverInventory(value DriverInventory) string {
	value.EvidenceDigest = ""
	raw, _ := json.Marshal(value)
	return spec.DigestString(string(raw))
}
