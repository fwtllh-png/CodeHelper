package policy

import (
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestApprovalCacheTypedFileGrantRequiresExactPathSet(t *testing.T) {
	now := time.Unix(5000, 0)
	cache := NewApprovalCache()
	a := tool.Resource{Kind: "file", Path: "a.go", Access: tool.AccessWrite}
	b := tool.Resource{Kind: "file", Path: "b.go", Access: tool.AccessWrite}
	c := tool.Resource{Kind: "file", Path: "c.go", Access: tool.AccessWrite}

	first := Invocation{
		CallID: "1", Tool: "file_patch", Arguments: []byte(`{"diff":"ab"}`),
		Resources: []tool.Resource{a, b}, Capability: CapabilityWrite,
		Access: tool.AccessTree, Sandbox: tool.SandboxStrong, Journaled: true, Validated: true,
	}
	request, err := NewApprovalRequestForScope(first, ApprovalSession, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Add(request, ApprovalSession); err != nil {
		t.Fatal(err)
	}

	subset := Invocation{
		CallID: "2", Tool: "file_patch", Arguments: []byte(`{"diff":"a-only"}`),
		Resources: []tool.Resource{a}, Capability: CapabilityWrite,
		Access: tool.AccessTree, Sandbox: tool.SandboxStrong, Journaled: true, Validated: true,
	}
	if cache.MatchInvocation(subset, now) {
		t.Fatal("path subset must not inherit a broader transaction grant")
	}

	bothAgain := Invocation{
		CallID: "3", Tool: "file_patch", Arguments: []byte(`{"diff":"ab2"}`),
		Resources: []tool.Resource{a, b}, Capability: CapabilityWrite,
		Access: tool.AccessTree, Sandbox: tool.SandboxStrong, Journaled: true, Validated: true,
	}
	if !cache.MatchInvocation(bothAgain, now) {
		t.Fatal("full previously-approved set should skip ask")
	}

	partial := Invocation{
		CallID: "4", Tool: "file_patch", Arguments: []byte(`{"diff":"ac"}`),
		Resources: []tool.Resource{a, c}, Capability: CapabilityWrite,
		Access: tool.AccessTree, Sandbox: tool.SandboxStrong, Journaled: true, Validated: true,
	}
	if cache.MatchInvocation(partial, now) {
		t.Fatal("unapproved path must still ask")
	}
}
