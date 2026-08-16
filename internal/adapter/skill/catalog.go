package skill

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"
)

type Catalog struct {
	mu             sync.RWMutex
	entries        map[string]candidate
	order          []string
	issues         []Issue
	locale         string
	limits         Limits
	state          *StateStore
	verifier       AuthorityVerifier
	runtimeVersion string
	lock           *LockStore
	selectionMu    sync.Mutex
	selectionCache map[string]Selection
	selectionOrder []string
}

func Discover(options DiscoveryOptions) (*Catalog, error) {
	options.Limits = options.Limits.normalized()
	native, issues, err := discoverNative(options)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]candidate)
	var order []string
	for _, item := range native {
		if _, exists := entries[item.metadata.Name]; exists {
			continue
		}
		entries[item.metadata.Name] = item
		order = append(order, item.metadata.Name)
	}
	for _, snapshot := range options.Plugins {
		for _, item := range snapshot.cloneSkills() {
			if _, exists := entries[item.metadata.Name]; exists {
				continue
			}
			if item.plugin == "" || item.authority.validate() != nil {
				issues = append(issues, Issue{
					Path: item.path, Reason: "plugin skill snapshot authority is invalid",
				})
				continue
			}
			if options.Verifier == nil {
				itemVerifier := snapshot.verifier
				if itemVerifier == nil {
					issues = append(issues, Issue{
						Path: item.path, Reason: "plugin skill authority verifier is missing",
					})
					continue
				}
				item.verifier = itemVerifier
			}
			entries[item.metadata.Name] = item
			order = append(order, item.metadata.Name)
		}
	}
	return &Catalog{
		entries: entries, order: order, issues: append([]Issue(nil), issues...),
		locale: normalizeLocale(options.Locale), limits: options.Limits,
		state: options.State, verifier: options.Verifier,
		runtimeVersion: normalizeRuntimeVersion(options.RuntimeVersion),
		lock:           options.Lock,
		selectionCache: make(map[string]Selection),
	}, nil
}

func (c *Catalog) Issues() []Issue {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Issue(nil), c.issues...)
}

func (c *Catalog) Summaries(ctx context.Context) []Summary {
	summaries, _ := c.List(ctx)
	return summaries
}

func (c *Catalog) List(ctx context.Context) ([]Summary, []Issue) {
	if c == nil {
		return nil, nil
	}
	entries, order, baseIssues := c.snapshot()
	state, stateErr := c.stateSnapshot()
	issues := append([]Issue(nil), baseIssues...)
	if stateErr != nil {
		issues = append(issues, Issue{
			Path: c.state.Path(), Reason: stateErr.Error(),
		})
	}
	var result []Summary
	locked := c.lockEntries()
	for _, name := range order {
		item := entries[name]
		if !enabledFor(item, state, stateErr) {
			continue
		}
		if item.source == SourcePlugin {
			if err := c.verify(ctx, item); err != nil {
				issues = append(issues, Issue{Path: item.path, Reason: err.Error()})
				continue
			}
		}
		result = append(result, c.summary(item, lockMatches(item, locked[name])))
	}
	if err := c.Verify(ctx); err != nil {
		path := "skill.lock.json"
		if c.lock != nil {
			path = c.lock.Path()
		}
		issues = append(issues, Issue{Path: path, Reason: err.Error()})
	}
	return result, issues
}

func (c *Catalog) Load(ctx context.Context, name string) (Loaded, error) {
	plan, err := c.LoadPlan(ctx, name)
	if err != nil {
		return Loaded{}, err
	}
	if len(plan) == 0 {
		return Loaded{}, fmt.Errorf("skill %q resolved to an empty plan", name)
	}
	return plan[len(plan)-1], nil
}

func (c *Catalog) SetEnabled(name string, enabled bool) error {
	if c == nil || c.state == nil {
		return errors.New("skill enable state store is not configured")
	}
	return c.state.SetEnabled(name, enabled)
}

func (c *Catalog) summary(item candidate, locked bool) Summary {
	version := legacySkillVersion
	compatibility := ""
	if item.manifest != nil {
		version = item.manifest.Version
		compatibility = item.manifest.CodeHelper
	}
	return Summary{
		Name: item.metadata.Name, Description: item.metadata.DescriptionFor(c.locale),
		Source: item.source, Path: item.path, Plugin: item.plugin,
		Version: version, Compatibility: compatibility,
		Digest: item.digest, Locked: locked,
		Handle: skillHandle(item), PackageHandle: skillPackageHandle(item),
		ResourceHandle: skillResourceHandle(item),
		ModelInvocable: !item.metadata.DisableModelInvocation,
	}
}

func (c *Catalog) stateSnapshot() (map[string]bool, error) {
	if c.state == nil {
		return map[string]bool{}, nil
	}
	return c.state.Snapshot()
}

func enabledFor(item candidate, state map[string]bool, stateErr error) bool {
	if stateErr != nil {
		return item.source != SourcePlugin
	}
	enabled, exists := state[item.metadata.Name]
	return !exists || enabled
}

func (c *Catalog) verify(ctx context.Context, item candidate) error {
	verifier := c.verifier
	if verifier == nil {
		verifier = item.verifier
	}
	if verifier == nil {
		return errors.New("plugin skill authority verifier is missing")
	}
	return verifier.VerifySkillAuthority(ctx, item.authority)
}

func (c *Catalog) snapshot() (map[string]candidate, []string, []Issue) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries := make(map[string]candidate, len(c.entries))
	maps.Copy(entries, c.entries)
	return entries, append([]string(nil), c.order...), append([]Issue(nil), c.issues...)
}

func (c *Catalog) Names() []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := append([]string(nil), c.order...)
	sort.Strings(result)
	return result
}
