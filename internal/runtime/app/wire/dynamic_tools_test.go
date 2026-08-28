package wire

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
)

func TestTrustedDynamicToolsAreExplicitAndRequireToolRuntime(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join(
		"..", "..", "..", "..", "testdata", "providers", "openai",
	))
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	tools := false
	_, err = NewExec(t.Context(), ExecOptions{
		FixturePath: fixture, TrustedDynamicTools: true,
		ConfigOverrides: config.Overrides{Workspace: &workspace, Tools: &tools},
	})
	if err == nil {
		t.Fatal("trusted dynamic tools started without the guarded tool runtime")
	}

	tools = true
	session, err := NewExec(t.Context(), withNonDurableTestJournal(t, ExecOptions{
		FixturePath: fixture, TrustedDynamicTools: true, Permission: "bypass",
		ConfigOverrides: config.Overrides{Workspace: &workspace, Tools: &tools},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if session.DynamicTools() == nil {
		t.Fatal("explicit trusted dynamic tools flag did not create the manager")
	}
	var dynamicReceipt *ContributionReceipt
	for _, receipt := range session.ContributionReceipts() {
		if receipt.Contributor == "dynamic-tools" {
			value := receipt
			dynamicReceipt = &value
			break
		}
	}
	if dynamicReceipt == nil ||
		len(dynamicReceipt.Outputs) != 1 ||
		dynamicReceipt.Outputs[0] != "dynamic-tool-manager" {
		t.Fatalf("dynamic contribution receipt = %+v", dynamicReceipt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedDynamicToolsDefaultToDisabled(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join(
		"..", "..", "..", "..", "testdata", "providers", "openai",
	))
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	tools := true
	session, err := NewExec(t.Context(), withNonDurableTestJournal(t, ExecOptions{
		FixturePath: fixture, Permission: "bypass",
		ConfigOverrides: config.Overrides{Workspace: &workspace, Tools: &tools},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if session.DynamicTools() != nil {
		t.Fatal("dynamic tools were enabled without explicit host authority")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
