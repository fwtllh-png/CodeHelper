package skill

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSelectionThousandSkillsIsDeterministicBoundedAndSavesTokens(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	for index := range 1000 {
		name := fmt.Sprintf("skill-%04d", index)
		description := fmt.Sprintf(
			"Handle deterministic workflow category %04d and related diagnostics.",
			index,
		)
		if index == 731 {
			name = "terraform-diagnostics"
			description = "Diagnose terraform deployment plans and infrastructure failures."
		}
		writeSkill(t, root, name, description, "SECRET BODY "+name)
	}
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := SelectionRequest{
		Query: "please diagnose the terraform infrastructure deployment failure",
		Mode:  SelectionCandidate, Limit: 20,
	}
	first, err := catalog.Select(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.Select(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Candidates, second.Candidates) {
		t.Fatal("selector output changed for identical input")
	}
	if len(first.Visible) == 0 || len(first.Visible) > 20 {
		t.Fatalf("visible candidate count = %d", len(first.Visible))
	}
	if first.Visible[0].Name != "terraform-diagnostics" {
		t.Fatalf("top candidate = %+v", first.Visible[0])
	}
	if first.Metrics.TokenSavings < 0.80 {
		t.Fatalf("token savings = %.4f", first.Metrics.TokenSavings)
	}
	if !second.Metrics.CacheHit {
		t.Fatal("second deterministic selection did not hit bounded cache")
	}
	for _, summary := range first.Visible {
		if strings.Contains(summary.Description, "SECRET BODY") {
			t.Fatal("skill body entered candidate metadata")
		}
	}
}

func TestSelectionExplicitMentionAndRequiredSkillHavePerfectRecall(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	writeSkill(t, root, "code-review", "Review code for defects.", "review body")
	writeSkill(t, root, "release", "Publish a release.", "release body")
	writeSkill(t, root, "unrelated", "Format prose.", "unrelated body")
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	golden := []struct {
		query    string
		required []string
		critical string
	}{
		{query: "use @code-review now", critical: "code-review"},
		{query: "publish the release", critical: "release"},
		{query: "format this", required: []string{"code-review"}, critical: "code-review"},
	}
	for _, item := range golden {
		selection, selectErr := catalog.Select(
			context.Background(),
			SelectionRequest{
				Query: item.query, Required: item.required,
				Mode: SelectionCandidate, Limit: 2,
			},
		)
		if selectErr != nil {
			t.Fatal(selectErr)
		}
		if !summaryNamed(selection.Candidates, item.critical) {
			t.Fatalf(
				"critical skill %q missing from %+v",
				item.critical, selection.Candidates,
			)
		}
	}
}

func TestSelectionShadowRecordsCandidatesWithoutChangingVisibleCatalog(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		writeSkill(t, root, name, "Operate "+name, name+" body")
	}
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := catalog.Select(t.Context(), SelectionRequest{
		Query: "operate beta", Mode: SelectionShadow, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Candidates) != 1 ||
		selection.Candidates[0].Name != "beta" ||
		len(selection.Visible) != 3 {
		t.Fatalf("shadow selection = %+v", selection)
	}
}

func TestSkillHandleRejectsDigestDrift(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	writeSkill(t, root, "review", "Review code.", "original body")
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := catalog.HandleForName(t.Context(), "review")
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, root, "review", "Review code.", "changed body")
	if _, err := catalog.LoadHandle(t.Context(), handle); err == nil {
		t.Fatal("digest-drifted skill handle was accepted")
	}
}

func summaryNamed(values []Summary, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}
