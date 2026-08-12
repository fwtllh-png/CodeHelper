package wire

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type buildModuleFunc struct {
	name string
	fn   func(context.Context, *buildState) error
}

func (m buildModuleFunc) Name() string { return m.name }

func (m buildModuleFunc) Build(
	ctx context.Context,
	state *buildState,
) error {
	return m.fn(ctx, state)
}

func TestBuildModulesPreserveOrderAndStopAtFailure(t *testing.T) {
	failure := errors.New("stop")
	var order []string
	module := func(name string, err error) buildModule {
		return buildModuleFunc{name: name, fn: func(
			context.Context,
			*buildState,
		) error {
			order = append(order, name)
			return err
		}}
	}
	err := buildModules(
		t.Context(),
		&buildState{},
		module("first", nil),
		module("second", failure),
		module("unreachable", nil),
	)
	if !errors.Is(err, failure) {
		t.Fatalf("build error = %v", err)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("module order = %v, want %v", order, want)
	}
	if !strings.Contains(err.Error(), `module second`) {
		t.Fatalf("module identity missing from error: %v", err)
	}
}

func TestNewExecRollsBackResourcesWhenModuleFails(t *testing.T) {
	failure := errors.New("construction failed")
	var closed atomic.Int32
	modules := []buildModule{
		buildModuleFunc{name: "resource", fn: func(
			_ context.Context,
			state *buildState,
		) error {
			return state.session.resources.Add(
				"test-resource",
				func(context.Context) error {
					closed.Add(1)
					return nil
				},
			)
		}},
		buildModuleFunc{name: "failure", fn: func(
			context.Context,
			*buildState,
		) error {
			return failure
		}},
	}
	session, err := newExec(t.Context(), ExecOptions{}, modules)
	if session != nil || !errors.Is(err, failure) {
		t.Fatalf("NewExec = (%v, %v)", session, err)
	}
	if closed.Load() != 1 {
		t.Fatalf("rollback close count = %d, want 1", closed.Load())
	}
}

func TestDefaultBuildModuleOrder(t *testing.T) {
	modules := defaultBuildModules()
	names := make([]string, 0, len(modules))
	for _, module := range modules {
		names = append(names, module.Name())
	}
	want := []string{
		"config",
		"provider",
		"platform",
		"persistence",
		"builtin-tools",
		"extension-tools",
		"security",
		"orchestration",
		"agent",
		"runtime",
		"background",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("module order = %v, want %v", names, want)
	}
}

func TestExtensionContributorIDsAreUnique(t *testing.T) {
	module := newExtensionToolsModule()
	seen := make(map[string]struct{}, len(module.contributors))
	for _, contributor := range module.contributors {
		id := contributor.ID()
		if id == "" {
			t.Fatal("empty contributor ID")
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate contributor ID %q", id)
		}
		seen[id] = struct{}{}
	}
	for _, required := range []string{
		"plugin-bundle",
		"plugin-registry",
		"skills",
		"memory",
		"task-automation",
		"hooks",
		"mcp",
	} {
		if _, exists := seen[required]; !exists {
			t.Errorf("required contributor %q is missing", required)
		}
	}
}

func TestNewExecRemainsConstructionOnlyOrchestration(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("runtime.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "func NewExec(")
	if start < 0 {
		t.Fatal("NewExec source boundary was not found")
	}
	end := strings.Index(text[start:], "\nfunc configuredDiagnosticCommands")
	if end < 0 {
		t.Fatal("NewExec source boundary was not found")
	}
	newExec := text[start : start+end]
	for _, forbidden := range []string{
		"fixture.Start(",
		"builtin.NewWithIndex(",
		"toolguard.New(",
		"agentengine.New(",
		"NewPersistentRuntime(",
		"startScheduler(",
	} {
		if strings.Contains(newExec, forbidden) {
			t.Errorf("NewExec owns construction call %q", forbidden)
		}
	}
}

func TestWireUsesOneOrchestrationToolContributor(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("modules_extensions.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		`internal/adapter/tool/automation`,
		`internal/adapter/tool/task`,
		"automationtool.Register(",
		"tasktool.Register(",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("wire reintroduced orchestration tool owner %q", forbidden)
		}
	}
	if !strings.Contains(text, "orchestrationextension.Contribute(") {
		t.Fatal("wire does not use the orchestration tool contributor")
	}
}

func TestModuleClosuresDoNotRetainBuildState(t *testing.T) {
	files, err := filepath.Glob("modules_*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		fileset := token.NewFileSet()
		file, parseErr := parser.ParseFile(
			fileset,
			path,
			nil,
			0,
		)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.FuncLit)
			if !ok {
				return true
			}
			ast.Inspect(literal.Body, func(inner ast.Node) bool {
				selector, selectorOK := inner.(*ast.SelectorExpr)
				if !selectorOK {
					return true
				}
				identifier, identifierOK := selector.X.(*ast.Ident)
				if identifierOK && identifier.Name == "state" {
					t.Errorf(
						"%s retains buildState in a closure at %s",
						path,
						fileset.Position(selector.Pos()),
					)
				}
				return true
			})
			return false
		})
	}
}
