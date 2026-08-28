package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/testutil/tooltest"
)

func TestSkillDiscoveryToolsPageAndReadAuthorityBoundContent(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	for index := range 25 {
		name := fmt.Sprintf("skill-%02d", index)
		body := "body " + name
		if index == 0 {
			body = strings.Repeat("bounded content\n", 5000)
		}
		writeToolSkill(t, root, name, "Operate "+name, body)
	}
	catalog, err := skillruntime.Discover(skillruntime.DiscoveryOptions{
		Workspace: workspace, UserHome: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterDiscovery(registry, catalog); err != nil {
		t.Fatal(err)
	}
	first, err := tooltest.Execute(context.Background(), registry, tool.Call{
		Name: "skills.list", Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Skills     []listedSkill `json:"skills"`
		NextCursor string        `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(first.Content), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Skills) != 20 || page.NextCursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	secondArgs, _ := json.Marshal(listInput{Cursor: page.NextCursor})
	second, err := tooltest.Execute(context.Background(), registry, tool.Call{
		Name: "skills.list", Arguments: secondArgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	var secondPage struct {
		Skills []listedSkill `json:"skills"`
	}
	if err := json.Unmarshal([]byte(second.Content), &secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Skills) != 5 {
		t.Fatalf("second page count = %d", len(secondPage.Skills))
	}

	target := page.Skills[0]
	for name, handle := range map[string]string{
		"skill":    target.Handle,
		"package":  target.PackageHandle,
		"resource": target.ResourceHandle,
	} {
		t.Run(name+"_handle", func(t *testing.T) {
			readArgs, _ := json.Marshal(readInput{Handle: handle})
			read, readErr := tooltest.Execute(context.Background(), registry, tool.Call{
				Name: "skills.read", Arguments: readArgs,
			})
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(read.Content) > skillReadBytes ||
				read.Metadata["next_cursor"] == "" ||
				read.Metadata["handle"] != target.Handle ||
				read.Admission == nil || read.Admission.Kind != "skill" {
				t.Fatalf("read result = %+v", read)
			}
		})
	}
	staleArgs, _ := json.Marshal(readInput{
		Handle: "skh_" + strings.Repeat("0", 40),
	})
	if _, err := tooltest.Execute(context.Background(), registry, tool.Call{
		Name: "skills.read", Arguments: staleArgs,
	}); err == nil {
		t.Fatal("mismatched authority-bound resource was accepted")
	} else if hint, ok := tool.RecoveryHintFromError(err); !ok ||
		hint.ErrorCategory != skillruntime.ErrorCategoryHandleInvalid ||
		hint.RequiredAction != "skills_list" || hint.RetryOriginal {
		t.Fatalf("stale skill recovery hint = %+v, found = %t", hint, ok)
	}
}

func TestSkillDiscoveryToolSchemaFitsCE7RegressionBudget(t *testing.T) {
	type schema struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Input       map[string]any `json:"input_schema"`
	}
	var total int
	for _, descriptor := range []tool.Descriptor{
		listDescriptor(), readDescriptor(),
	} {
		data, err := json.Marshal(schema{
			Name: descriptor.Name, Description: descriptor.Description,
			Input: descriptor.InputSchema,
		})
		if err != nil {
			t.Fatal(err)
		}
		total += len(data)
	}
	const maxSchemaDeltaBytes = 640
	if total > maxSchemaDeltaBytes {
		t.Fatalf(
			"skill discovery schema delta = %d bytes, maximum = %d",
			total, maxSchemaDeltaBytes,
		)
	}
	t.Logf("skill discovery schema delta = %d bytes", total)
}

func writeToolSkill(
	t *testing.T,
	root, name, description, body string,
) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description +
		"\n---\n" + body + "\n"
	if err := os.WriteFile(
		filepath.Join(directory, "SKILL.md"), []byte(content), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}
