package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensioncontrol"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type ControlPlane struct {
	mu          sync.Mutex
	plugins     *pluginruntime.Registry
	skills      *skillruntime.Catalog
	store       *extensioncontrol.Store
	observed    *controlObservability
	subscribers map[uint64]chan protocol.ExtensionControlEvent
	next        uint64
}

func NewControlPlane(
	plugins *pluginruntime.Registry,
	skills *skillruntime.Catalog,
	store *extensioncontrol.Store,
) (*ControlPlane, error) {
	if plugins == nil || skills == nil || store == nil {
		return nil, errors.New("extension control dependencies are required")
	}
	return &ControlPlane{
		plugins: plugins, skills: skills, store: store,
		observed:    newControlObservability(),
		subscribers: make(map[uint64]chan protocol.ExtensionControlEvent),
	}, nil
}

func (c *ControlPlane) Submit(
	ctx context.Context,
	operation protocol.ExtensionControlOperation,
) (result protocol.ExtensionControlResult, returnErr error) {
	if c == nil {
		return protocol.ExtensionControlResult{}, errors.New(
			"extension control plane is unavailable",
		)
	}
	started := c.observed.begin()
	defer func() {
		status, alert := "committed", ""
		if returnErr != nil {
			status, alert = "failed", "operation_failed"
		} else if result.Duplicate {
			status = "duplicate"
		} else if result.Receipt != nil && result.Receipt.Status == "reconciled" {
			status = "reconciled"
		}
		c.observed.finish(operation, started, status, alert)
	}()
	if err := operation.Validate(); err != nil {
		return protocol.ExtensionControlResult{}, err
	}
	digest, digestErr := controlOperationDigest(operation)
	if digestErr != nil {
		return protocol.ExtensionControlResult{}, digestErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok, lookupErr := c.store.Lookup(ctx, operation.ID); lookupErr != nil {
		return protocol.ExtensionControlResult{}, lookupErr
	} else if ok {
		if existing.Digest != digest {
			return protocol.ExtensionControlResult{}, errors.New(
				"extension operation id conflicts with prior payload",
			)
		}
		if existing.Status == "prepared" {
			return c.reconcilePrepared(ctx, operation, digest)
		}
		result = existing.Result
		result.Duplicate = true
		return result, nil
	}
	var detail json.RawMessage
	if !operation.Query() {
		if prepareErr := c.store.Prepare(
			ctx, operation.ID, digest,
		); prepareErr != nil {
			return protocol.ExtensionControlResult{}, prepareErr
		}
		var err error
		detail, err = c.mutate(ctx, operation)
		if err != nil {
			return protocol.ExtensionControlResult{}, errors.Join(
				err, c.store.Abort(ctx, operation.ID, digest),
			)
		}
	}
	projections, err := c.project(ctx, operation.Kind, operation.Name)
	if err != nil {
		return protocol.ExtensionControlResult{}, err
	}
	if operation.Query() {
		revision, events, snapshotErr := c.store.Snapshot(ctx)
		result = protocol.ExtensionControlResult{
			OperationID: operation.ID, Revision: revision,
			Extensions: projections,
		}
		if operation.Action == protocol.ExtensionActionReceipts {
			for _, event := range events {
				if operation.Name == "" ||
					event.Receipt.Name == operation.Name {
					result.Receipts = append(result.Receipts, event.Receipt)
				}
			}
		}
		if operation.Action == protocol.ExtensionActionHealth {
			diagnostics := c.observed.snapshot()
			result.Diagnostics = &diagnostics
		}
		return result, snapshotErr
	}
	now := time.Now().UTC()
	receiptDigest := digestProjection(operation, projections)
	receipt := protocol.ExtensionControlReceipt{
		OperationID: operation.ID, Action: operation.Action,
		Kind: operation.Kind, Name: operation.Name, Status: "committed",
		Digest: receiptDigest, OccurredAt: now,
	}
	var projection *protocol.ExtensionProjection
	if len(projections) != 0 {
		value := projections[0]
		projection = &value
	}
	event := protocol.ExtensionControlEvent{
		OperationID: operation.ID, Action: operation.Action,
		Projection: projection, Receipt: receipt, OccurredAt: now,
	}
	result = protocol.ExtensionControlResult{
		OperationID: operation.ID, Extensions: projections,
		Detail: detail, Receipt: &receipt,
	}
	if commitErr := c.store.Commit(
		ctx, operation.ID, digest, result, event,
	); commitErr != nil {
		return protocol.ExtensionControlResult{}, commitErr
	}
	stored, ok, err := c.store.Lookup(ctx, operation.ID)
	if err != nil || !ok {
		return protocol.ExtensionControlResult{}, errors.Join(
			err, errors.New("extension operation receipt was not committed"),
		)
	}
	c.publish(stored.Result, event)
	return stored.Result, nil
}

func (c *ControlPlane) reconcilePrepared(
	ctx context.Context,
	operation protocol.ExtensionControlOperation,
	digest string,
) (protocol.ExtensionControlResult, error) {
	projections, err := c.project(ctx, operation.Kind, operation.Name)
	if err != nil {
		return protocol.ExtensionControlResult{}, err
	}
	now := time.Now().UTC()
	receipt := protocol.ExtensionControlReceipt{
		OperationID: operation.ID, Action: operation.Action,
		Kind: operation.Kind, Name: operation.Name, Status: "reconciled",
		Digest: digestProjection(operation, projections), OccurredAt: now,
	}
	var projection *protocol.ExtensionProjection
	if len(projections) != 0 {
		value := projections[0]
		projection = &value
	}
	event := protocol.ExtensionControlEvent{
		OperationID: operation.ID, Action: operation.Action,
		Projection: projection, Receipt: receipt, OccurredAt: now,
	}
	result := protocol.ExtensionControlResult{
		OperationID: operation.ID, Extensions: projections, Receipt: &receipt,
	}
	if commitErr := c.store.Commit(
		ctx, operation.ID, digest, result, event,
	); commitErr != nil {
		return protocol.ExtensionControlResult{}, commitErr
	}
	stored, ok, err := c.store.Lookup(ctx, operation.ID)
	if err != nil || !ok {
		return protocol.ExtensionControlResult{}, errors.Join(
			err, errors.New("reconciled extension receipt was not committed"),
		)
	}
	c.publish(stored.Result, event)
	return stored.Result, nil
}

func (c *ControlPlane) Snapshot(
	ctx context.Context,
	kind protocol.ExtensionControlKind,
) (protocol.ExtensionControlResult, error) {
	operation, err := protocol.NewExtensionControlOperation(
		kind, protocol.ExtensionActionList,
	)
	if err != nil {
		return protocol.ExtensionControlResult{}, err
	}
	return c.Submit(ctx, operation)
}

func (c *ControlPlane) Replay(
	ctx context.Context,
	after uint64,
	limit int,
) ([]protocol.ExtensionControlEvent, bool, error) {
	if limit <= 0 || limit > 1000 {
		return nil, false, errors.New("extension replay limit is invalid")
	}
	_, events, err := c.store.Snapshot(ctx)
	if err != nil {
		return nil, false, err
	}
	start := sort.Search(len(events), func(index int) bool {
		return events[index].Sequence > after
	})
	end := min(start+limit, len(events))
	return append([]protocol.ExtensionControlEvent(nil), events[start:end]...),
		end < len(events), nil
}

func (c *ControlPlane) Subscribe(
	buffer int,
) (<-chan protocol.ExtensionControlEvent, func(), error) {
	if buffer <= 0 || buffer > 1024 {
		return nil, nil, errors.New("extension subscriber buffer is invalid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := c.next
	channel := make(chan protocol.ExtensionControlEvent, buffer)
	c.subscribers[id] = channel
	var once sync.Once
	return channel, func() {
		once.Do(func() {
			c.mu.Lock()
			if current := c.subscribers[id]; current != nil {
				delete(c.subscribers, id)
				close(current)
			}
			c.mu.Unlock()
		})
	}, nil
}

func (c *ControlPlane) mutate(
	ctx context.Context,
	operation protocol.ExtensionControlOperation,
) (json.RawMessage, error) {
	if operation.Kind == protocol.ExtensionControlSkill {
		return c.mutateSkill(ctx, operation)
	}
	if operation.Action != protocol.ExtensionActionRevoke &&
		operation.Action != protocol.ExtensionActionSecurityRevoke {
		if err := c.plugins.Reload(); err != nil {
			return nil, err
		}
	}
	switch operation.Action {
	case protocol.ExtensionActionTrust:
		_, err := c.plugins.Trust(operation.Name)
		return nil, err
	case protocol.ExtensionActionEnable:
		return nil, c.plugins.Enable(operation.Name)
	case protocol.ExtensionActionDisable:
		return nil, c.plugins.Disable(operation.Name)
	case protocol.ExtensionActionCapabilityEnable:
		return nil, c.plugins.EnableCapability(operation.Name, operation.Capability)
	case protocol.ExtensionActionCapabilityDisable:
		return nil, c.plugins.DisableCapability(operation.Name, operation.Capability)
	case protocol.ExtensionActionRevoke:
		return nil, c.plugins.Revoke(operation.Name)
	case protocol.ExtensionActionSecurityRevoke:
		return nil, c.plugins.SecurityRevoke(operation.Name)
	case protocol.ExtensionActionInstall:
		_, err := c.plugins.Install(ctx, operation.Name, operation.VersionValue)
		return nil, err
	case protocol.ExtensionActionUpdate:
		_, err := c.plugins.Update(ctx, operation.Name, operation.VersionValue)
		return nil, err
	case protocol.ExtensionActionRollback:
		_, err := c.plugins.Rollback(operation.Name)
		return nil, err
	case protocol.ExtensionActionLint:
		result, err := pluginruntime.LintBundle(operation.Name)
		if err != nil {
			return nil, err
		}
		detail, err := json.Marshal(result)
		return detail, err
	default:
		return nil, fmt.Errorf("unsupported plugin action %q", operation.Action)
	}
}

func (c *ControlPlane) mutateSkill(
	ctx context.Context,
	operation protocol.ExtensionControlOperation,
) (json.RawMessage, error) {
	switch operation.Action {
	case protocol.ExtensionActionEnable:
		return nil, c.skills.SetEnabled(operation.Name, true)
	case protocol.ExtensionActionDisable, protocol.ExtensionActionRevoke:
		return nil, c.skills.SetEnabled(operation.Name, false)
	case protocol.ExtensionActionLint:
		result, err := skillruntime.Lint(operation.Name, "")
		if err != nil {
			return nil, err
		}
		detail, err := json.Marshal(result)
		return detail, err
	case protocol.ExtensionActionLock:
		_, err := c.skills.WriteLock(ctx)
		return nil, err
	case protocol.ExtensionActionVerify:
		return nil, c.skills.Verify(ctx)
	default:
		return nil, fmt.Errorf("unsupported skill action %q", operation.Action)
	}
}

func (c *ControlPlane) project(
	ctx context.Context,
	kind protocol.ExtensionControlKind,
	name string,
) ([]protocol.ExtensionProjection, error) {
	var result []protocol.ExtensionProjection
	if kind == protocol.ExtensionControlPlugin || kind == protocol.ExtensionControlAll {
		plugins, err := c.pluginProjections(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, plugins...)
	}
	if kind == protocol.ExtensionControlSkill || kind == protocol.ExtensionControlAll {
		skills, err := c.skillProjections(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, skills...)
	}
	if name != "" {
		filtered := result[:0]
		for _, value := range result {
			if value.Name == name {
				filtered = append(filtered, value)
			}
		}
		result = filtered
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (c *ControlPlane) pluginProjections(
	ctx context.Context,
) ([]protocol.ExtensionProjection, error) {
	if err := c.plugins.Reload(); err != nil {
		return nil, err
	}
	values, err := c.plugins.List()
	if err != nil {
		return nil, err
	}
	bundles, _ := c.plugins.CapabilityBundles(ctx)
	bundleByName := make(map[string]pluginruntime.CompiledBundle, len(bundles))
	for _, bundle := range bundles {
		bundleByName[bundle.Plugin] = bundle
	}
	lifecycles, err := c.plugins.LifecycleSnapshots()
	if err != nil {
		return nil, err
	}
	lifecycleByName := make(
		map[string]pluginruntime.LifecycleSnapshot,
		len(lifecycles),
	)
	for _, lifecycle := range lifecycles {
		lifecycleByName[lifecycle.Name] = lifecycle
	}
	result := make([]protocol.ExtensionProjection, 0, len(values))
	for _, value := range values {
		candidate := value.Candidate
		projection := protocol.ExtensionProjection{
			Kind: protocol.ExtensionControlPlugin, Name: candidate.Name,
			Version: candidate.Manifest.Version, Source: candidate.Root.String(),
			Publisher: candidate.Manifest.Publisher,
			Digest:    candidate.Manifest.ExecutableSHA256,
			Enabled:   value.Enabled, Health: projectionHealth(value.Enabled),
			Trust: "untrusted",
		}
		if lifecycle, ok := lifecycleByName[candidate.Name]; ok {
			projection.Source = lifecycle.Source
			projection.Trust = lifecycle.Trust
		}
		if bundle, ok := bundleByName[candidate.Name]; ok {
			projection.Generation = bundle.Generation
			projection.Digest = bundle.Digest
			for _, capability := range bundle.Capabilities {
				projection.Capabilities = append(
					projection.Capabilities,
					protocol.ExtensionCapabilityProjection{
						ID: capability.ID, Kind: string(capability.Kind),
						Enabled:          capability.Enabled,
						SourceDigest:     capability.SourceDigest,
						PermissionDigest: capability.Authority.PermissionDigest,
						AuthorityToken:   capability.Authority.Token,
					},
				)
				projection.Permissions = append(
					projection.Permissions,
					capabilityPermissions(capability.Permissions)...,
				)
			}
		}
		projection.Permissions = sortedUniqueControl(projection.Permissions)
		result = append(result, projection)
	}
	return result, nil
}

func (c *ControlPlane) skillProjections(
	ctx context.Context,
) ([]protocol.ExtensionProjection, error) {
	values, err := c.skills.ControlSummaries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]protocol.ExtensionProjection, 0, len(values))
	for _, value := range values {
		summary := value.Summary
		result = append(result, protocol.ExtensionProjection{
			Kind: protocol.ExtensionControlSkill, Name: summary.Name,
			Version: summary.Version, Source: string(summary.Source),
			Digest: summary.Digest, Enabled: value.Enabled,
			Health: projectionHealth(value.Enabled),
			Trust:  "catalog", Permissions: []string{"content.read"},
			Capabilities: []protocol.ExtensionCapabilityProjection{{
				ID: summary.Handle, Kind: "skill", Enabled: value.Enabled,
				SourceDigest: summary.Digest, AuthorityToken: summary.Handle,
			}},
		})
	}
	return result, nil
}

func (c *ControlPlane) publish(
	result protocol.ExtensionControlResult,
	event protocol.ExtensionControlEvent,
) {
	if result.Receipt != nil {
		event.Sequence = result.Revision
		event.Receipt = *result.Receipt
		event.Receipt.Revision = result.Revision
	}
	for id, subscriber := range c.subscribers {
		select {
		case subscriber <- event:
		default:
			delete(c.subscribers, id)
			close(subscriber)
			c.observed.subscriberDropped()
		}
	}
}

func controlOperationDigest(
	operation protocol.ExtensionControlOperation,
) (string, error) {
	data, err := json.Marshal(operation)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func digestProjection(
	operation protocol.ExtensionControlOperation,
	values []protocol.ExtensionProjection,
) string {
	data, _ := json.Marshal(struct {
		Operation protocol.ExtensionControlOperation
		Values    []protocol.ExtensionProjection
	}{Operation: operation, Values: values})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func capabilityPermissions(value pluginruntime.CapabilityInventory) []string {
	result := append([]string(nil), value.Tools...)
	for _, root := range value.FilesystemRoots {
		result = append(result, "filesystem:"+root)
	}
	for _, host := range value.NetworkHosts {
		result = append(result, "network:"+host)
	}
	if value.AllowProcess {
		result = append(result, "process")
	}
	return result
}

func projectionHealth(enabled bool) string {
	if enabled {
		return "active"
	}
	return "inactive"
}

func sortedUniqueControl(values []string) []string {
	sort.Strings(values)
	write := 0
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || write > 0 && values[write-1] == value {
			continue
		}
		values[write] = value
		write++
	}
	return values[:write]
}
