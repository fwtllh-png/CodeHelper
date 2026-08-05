package protocol

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceIdentityValidatesLocalAndRemoteRoots(t *testing.T) {
	root := t.TempDir()
	local, err := NewWorkspaceIdentity(
		(&url.URL{Scheme: "file", Path: root}).String(),
		root,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if local.Version != WorkspaceIdentityVersion || len(local.RootID) != 64 {
		t.Fatalf("local identity = %+v", local)
	}
	remote, err := NewWorkspaceIdentity(
		"vscode-remote://ssh-remote+dev/workspace",
		"/workspace",
		"ssh-remote",
	)
	if err != nil {
		t.Fatal(err)
	}
	if remote.RemoteName != "ssh-remote" || remote.RootID == local.RootID {
		t.Fatalf("remote identity = %+v", remote)
	}
	forged := remote
	forged.RemoteName = "dev-container"
	if err := forged.Validate(); err == nil {
		t.Fatal("remote authority accepted a mismatched remote name")
	}
}

func TestWorkspaceIdentityFailsClosed(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	valid, err := NewWorkspaceIdentity(
		(&url.URL{Scheme: "file", Path: root}).String(),
		root,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*WorkspaceIdentity){
		"root id": func(value *WorkspaceIdentity) {
			value.RootID = strings.Repeat("0", 64)
		},
		"relative runtime path": func(value *WorkspaceIdentity) {
			value.RuntimePath = "workspace"
		},
		"local remote name": func(value *WorkspaceIdentity) {
			value.RemoteName = "ssh-remote"
		},
		"query": func(value *WorkspaceIdentity) {
			value.EditorURI += "?forged=true"
		},
		"unsupported scheme": func(value *WorkspaceIdentity) {
			value.EditorURI = "https://example.com/workspace"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("invalid identity accepted: %+v", candidate)
			}
		})
	}
}
