package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type ApprovalScope string

const (
	ApprovalOnce    ApprovalScope = "once"
	ApprovalSession ApprovalScope = "session"
	ApprovalAlways  ApprovalScope = "always"
)

type ApprovalRequest struct {
	RequestID       string          `json:"request_id"`
	CallID          string          `json:"call_id"`
	Tool            string          `json:"tool"`
	Arguments       json.RawMessage `json:"arguments"`
	ArgumentsDigest string          `json:"arguments_digest"`
	Resources       []tool.Resource `json:"resources"`
	Scope           ApprovalScope   `json:"scope"`
	Fingerprint     string          `json:"fingerprint"`
	ExpiresAt       time.Time       `json:"expires_at"`
}

type ApprovalResponse struct {
	Approved  bool
	Scope     ApprovalScope
	ExpiresAt time.Time
}

type approvalEntry struct {
	expiresAt       time.Time
	once            bool
	baseFingerprint string
	sequence        uint64
}

type ApprovalCache struct {
	mu           sync.Mutex
	entries      map[string]approvalEntry
	resourceKeys map[string]approvalEntry // tool+resource.Key session grants (T3)
	limit        int
	next         uint64
}

const defaultApprovalCacheLimit = 1024

func NewApprovalCache() *ApprovalCache {
	return NewApprovalCacheWithLimit(defaultApprovalCacheLimit)
}

func NewApprovalCacheWithLimit(limit int) *ApprovalCache {
	if limit < 1 {
		limit = 1
	}
	return &ApprovalCache{
		entries: make(map[string]approvalEntry), resourceKeys: make(map[string]approvalEntry), limit: limit,
	}
}

func NewApprovalRequest(invocation Invocation, expiresAt time.Time) (ApprovalRequest, error) {
	return NewApprovalRequestForScope(invocation, ApprovalOnce, expiresAt)
}

func NewApprovalRequestForScope(
	invocation Invocation, scope ApprovalScope, expiresAt time.Time,
) (ApprovalRequest, error) {
	arguments, err := canonicalJSON(invocation.Arguments)
	if err != nil {
		return ApprovalRequest{}, err
	}
	resources := append([]tool.Resource(nil), invocation.Resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Key() < resources[j].Key() })
	resources = compactResources(resources)
	argumentsHash := sha256.Sum256(arguments)
	request := ApprovalRequest{
		CallID: invocation.CallID, Tool: invocation.Tool, Arguments: arguments,
		ArgumentsDigest: hex.EncodeToString(argumentsHash[:]),
		Resources:       resources, Scope: scope, ExpiresAt: expiresAt,
	}
	request.Fingerprint = approvalFingerprint(request)
	return request, nil
}

func (c *ApprovalCache) Add(request ApprovalRequest, scope ApprovalScope) error {
	if c == nil {
		return errors.New("approval cache is required")
	}
	if request.Fingerprint == "" || request.Fingerprint != approvalFingerprint(request) {
		return errors.New("approval fingerprint does not match request")
	}
	if request.ExpiresAt.IsZero() {
		return errors.New("approval expiry is required")
	}
	if scope != ApprovalOnce && scope != ApprovalSession && scope != ApprovalAlways {
		return errors.New("approval scope must be once, session, or always")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]approvalEntry)
	}
	if c.resourceKeys == nil {
		c.resourceKeys = make(map[string]approvalEntry)
	}
	if c.limit < 1 {
		c.limit = defaultApprovalCacheLimit
	}
	c.next++
	request.Scope = scope
	request.Fingerprint = approvalFingerprint(request)
	entry := approvalEntry{
		expiresAt: request.ExpiresAt, once: scope == ApprovalOnce,
		baseFingerprint: approvalBaseFingerprint(request), sequence: c.next,
	}
	c.entries[request.Fingerprint] = entry
	// Session/Always grants expand to per-resource keys so multi-path patches
	// skip only when every path is already covered.
	if scope == ApprovalSession || scope == ApprovalAlways {
		for _, resource := range request.Resources {
			key := resourceApprovalKey(request.Tool, resource.Key())
			c.resourceKeys[key] = approvalEntry{
				expiresAt: request.ExpiresAt, once: false,
				baseFingerprint: key, sequence: c.next,
			}
		}
	}
	for len(c.entries) > c.limit {
		var oldestKey string
		var oldestSequence uint64
		for key, value := range c.entries {
			if oldestKey == "" || value.sequence < oldestSequence {
				oldestKey, oldestSequence = key, value.sequence
			}
		}
		delete(c.entries, oldestKey)
	}
	return nil
}

func (c *ApprovalCache) MatchInvocation(invocation Invocation, now time.Time) bool {
	if c == nil {
		return false
	}
	if c.matchInvocationExact(invocation, now) {
		return true
	}
	if hostScoped, ok := HostScopedInvocation(invocation); ok {
		if c.matchInvocationExact(hostScoped, now) {
			return true
		}
	}
	return c.matchAllResourceKeys(invocation, now)
}

func (c *ApprovalCache) matchAllResourceKeys(invocation Invocation, now time.Time) bool {
	resources := append([]tool.Resource(nil), invocation.Resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Key() < resources[j].Key() })
	resources = compactResources(resources)
	if len(resources) == 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resourceKeys == nil {
		return false
	}
	for _, resource := range resources {
		key := resourceApprovalKey(invocation.Tool, resource.Key())
		entry, exists := c.resourceKeys[key]
		if !exists {
			return false
		}
		if !entry.expiresAt.After(now) {
			delete(c.resourceKeys, key)
			return false
		}
	}
	return true
}

func resourceApprovalKey(toolName, resourceKey string) string {
	return toolName + "\x00" + resourceKey
}

func (c *ApprovalCache) matchInvocationExact(invocation Invocation, now time.Time) bool {
	request, err := NewApprovalRequestForScope(invocation, ApprovalOnce, time.Time{})
	if err != nil {
		return false
	}
	base := approvalBaseFingerprint(request)
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if !entry.expiresAt.After(now) {
			delete(c.entries, key)
			continue
		}
		if entry.baseFingerprint != base {
			continue
		}
		if entry.once {
			delete(c.entries, key)
		}
		return true
	}
	return false
}

func (c *ApprovalCache) Match(request ApprovalRequest, now time.Time) bool {
	if c == nil || request.Fingerprint == "" || request.Fingerprint != approvalFingerprint(request) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists := c.entries[request.Fingerprint]
	if !exists {
		return false
	}
	if !entry.expiresAt.After(now) {
		delete(c.entries, request.Fingerprint)
		return false
	}
	if entry.once {
		delete(c.entries, request.Fingerprint)
	}
	return true
}

func (c *ApprovalCache) Purge(now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if !entry.expiresAt.After(now) {
			delete(c.entries, key)
		}
	}
	for key, entry := range c.resourceKeys {
		if !entry.expiresAt.After(now) {
			delete(c.resourceKeys, key)
		}
	}
}

func approvalFingerprint(request ApprovalRequest) string {
	hash := sha256.New()
	writeFingerprintField(hash, approvalBaseFingerprint(request))
	writeFingerprintField(hash, string(request.Scope))
	writeFingerprintField(hash, request.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return hex.EncodeToString(hash.Sum(nil))
}

func approvalBaseFingerprint(request ApprovalRequest) string {
	hash := sha256.New()
	writeFingerprintField(hash, request.Tool)
	arguments, err := canonicalJSON(request.Arguments)
	if err != nil {
		return ""
	}
	writeFingerprintField(hash, string(arguments))
	resources := append([]tool.Resource(nil), request.Resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Key() < resources[j].Key() })
	for _, resource := range compactResources(resources) {
		encoded, _ := json.Marshal(resource)
		writeFingerprintField(hash, string(encoded))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func compactResources(values []tool.Resource) []tool.Resource {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1].Key() != value.Key() {
			result = append(result, value)
		}
	}
	return result
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeFingerprintField(writer byteWriter, value string) {
	var length [8]byte
	size := uint64(len(value))
	for index := range length {
		length[7-index] = byte(size)
		size >>= 8
	}
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple JSON values")
	}
	return json.Marshal(value)
}
