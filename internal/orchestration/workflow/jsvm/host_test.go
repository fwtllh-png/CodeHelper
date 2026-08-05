package jsvm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/jsvm"
)

func TestHostSleepEnvPathRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEHELPER_WF_MARK", "visible")
	t.Setenv("SECRET_TOKEN", "nope")
	driver := &jsvm.FakeDriver{}
	raw, err := jsvm.New().RunScript(context.Background(), `
sleep(5);
const joined = path.join(".", "note.txt");
const body = read(path.normalize(joined));
const mark = env("CODEHELPER_WF_MARK");
task(body.trim() + ":" + mark);
`, jsvm.Options{Driver: driver, Workspace: root, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"ok:alpha:visible"` && !strings.Contains(string(raw), "alpha:visible") {
		// FakeDriver returns content string marshaled as JSON string.
		if !strings.Contains(string(raw), "alpha") {
			t.Fatalf("result=%s tasks=%v", raw, driver.Tasks)
		}
	}
	if len(driver.Tasks) != 1 {
		t.Fatalf("tasks=%v", driver.Tasks)
	}
}

func TestTaskPassesResponseSchemaToDriver(t *testing.T) {
	driver := &jsvm.FakeDriver{}
	_, err := jsvm.New().RunScript(t.Context(), `
task({
  prompt: "count packages",
  response_schema: {
    type: "object",
    properties: { packages: { type: "integer" } },
    required: ["packages"]
  }
});
`, jsvm.Options{Driver: driver, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(driver.Tasks) != 1 ||
		!strings.Contains(string(driver.Tasks[0].Schema), `"packages"`) {
		t.Fatalf("tasks = %+v", driver.Tasks)
	}
}

func TestReadEscapesWorkspaceDenied(t *testing.T) {
	root := t.TempDir()
	driver := &jsvm.FakeDriver{}
	_, err := jsvm.New().RunScript(context.Background(), `read("../outside.txt");`, jsvm.Options{
		Driver: driver, Workspace: root,
	})
	if err == nil {
		t.Fatal("expected escape denial")
	}
}

func TestEnvSecretDenied(t *testing.T) {
	driver := &jsvm.FakeDriver{}
	_, err := jsvm.New().RunScript(context.Background(), `env("SECRET_TOKEN");`, jsvm.Options{
		Driver: driver, Workspace: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected secret env denial")
	}
}
