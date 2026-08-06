package cli

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/spf13/cobra"
)

//go:embed setupfixture/*
var bundledSetupFixture embed.FS

type setupSelection struct {
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
	CredentialKind string `json:"credential_kind,omitempty"`
	CredentialName string `json:"credential_name,omitempty"`
}

type setupReport struct {
	protocol.Readiness
	OK           bool                `json:"ok"`
	Config       string              `json:"config"`
	DataDir      string              `json:"data_dir"`
	Created      []string            `json:"created"`
	Selection    setupSelection      `json:"selection"`
	Sandbox      wire.SandboxReport  `json:"sandbox"`
	ModelProbe   []wire.ProbeResult  `json:"model_probe,omitempty"`
	Constitution constitution.Status `json:"constitution"`
}

func runSetupFlow(
	ctx context.Context,
	cmd *cobra.Command,
	stdin io.Reader,
	stdout, stderr io.Writer,
	setCode func(int),
) {
	options, err := readSetupOptions(cmd, stdin, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: setup: %v\n", err)
		setCode(2)
		return
	}
	selection, err := resolveSetupSelection(options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: setup: %v\n", err)
		setCode(2)
		return
	}
	created, constitutionStatus, err := createSetupWorkspace(options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: setup: %v\n", err)
		setCode(1)
		return
	}
	if selection.Provider != "" {
		if err := writeSetupSelection(options.configPath, selection); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: setup: %v\n", err)
			setCode(1)
			return
		}
	}

	checks := setupSelectionChecks(selection)
	sandbox := wire.ProbeSandbox()
	for _, check := range wire.RuntimeReadiness(sandbox) {
		if check.ID == "runtime.sandbox" {
			checks = append(checks, check)
			break
		}
	}

	operationFailed := false
	if options.skipFixture {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "setup.fixture", Status: protocol.ReadinessDegraded,
			Reason: "hermetic Runtime fixture verification was skipped",
			Impact: "the configured binary-to-runtime path was not exercised",
			Action: "rerun setup without --skip-fixture",
		})
	} else if err := verifySetupFixture(
		ctx,
		options.configPath,
		options.workspace,
		options.fixturePath,
	); err != nil {
		operationFailed = true
		checks = append(checks, protocol.ReadinessCheck{
			ID: "setup.fixture", Status: protocol.ReadinessBlocked,
			Reason: "hermetic Runtime fixture verification failed",
			Impact: "the first CodeHelper turn is not known to work",
			Action: "fix the reported Runtime error and rerun setup",
		})
		_, _ = fmt.Fprintf(stderr, "codehelper: setup fixture: %v\n", err)
	} else {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "setup.fixture", Status: protocol.ReadinessReady,
			Reason: "hermetic Runtime fixture completed successfully",
		})
	}

	var probeResults []wire.ProbeResult
	if options.probeCapabilities != "" {
		probeResults, err = runSetupModelProbe(ctx, options, selection)
		if err != nil {
			operationFailed = true
			checks = append(checks, protocol.ReadinessCheck{
				ID: "setup.model_probe", Status: protocol.ReadinessBlocked,
				Reason: "explicit model capability probe failed",
				Impact: "the selected live model route is not verified",
				Action: "check the credential reference and provider connectivity, then rerun setup",
			})
			_, _ = fmt.Fprintf(stderr, "codehelper: setup model probe: %v\n", err)
		} else {
			checks = append(checks, modelProbeReadiness(probeResults))
		}
	} else {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "setup.model_probe", Status: protocol.ReadinessDegraded,
			Reason: "live model capability probe was not requested",
			Impact: "catalog capabilities were not confirmed against the provider",
			Action: "rerun setup with --probe-capabilities reasoning or use model probe explicitly",
		})
	}

	readiness, err := protocol.NewReadiness(checks...)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: setup readiness: %v\n", err)
		setCode(1)
		return
	}
	report := setupReport{
		Readiness:    readiness,
		OK:           !operationFailed,
		Config:       options.configPath,
		DataDir:      options.dataDir,
		Created:      created,
		Selection:    selection,
		Sandbox:      sandbox,
		ModelProbe:   probeResults,
		Constitution: constitutionStatus,
	}
	if options.asJSON {
		_ = json.NewEncoder(stdout).Encode(report)
	} else {
		writeSetupText(stdout, report)
	}
	switch {
	case operationFailed:
		setCode(1)
	case options.requireReady:
		setCode(readiness.ExitCode())
	default:
		setCode(0)
	}
}

type setupOptions struct {
	workspace         string
	configPath        string
	dataDir           string
	provider          string
	model             string
	credentialKind    string
	credentialName    string
	probeCapabilities string
	fixturePath       string
	profile           config.Profile
	force             bool
	asJSON            bool
	interactive       bool
	skipFixture       bool
	requireReady      bool
}

func readSetupOptions(
	cmd *cobra.Command,
	stdin io.Reader,
	stderr io.Writer,
) (setupOptions, error) {
	getString := func(name string) string {
		value, _ := cmd.Flags().GetString(name)
		return strings.TrimSpace(value)
	}
	options := setupOptions{
		workspace: getString("workspace"), configPath: getString("config"),
		dataDir: getString("data-dir"), provider: getString("provider"),
		model: getString("model"), credentialKind: getString("credential-kind"),
		credentialName:    getString("credential-name"),
		probeCapabilities: getString("probe-capabilities"),
		fixturePath:       getString("provider-fixture"),
	}
	profile, err := config.ParseProfile(getString("profile"))
	if err != nil {
		return setupOptions{}, err
	}
	options.profile = profile
	options.force, _ = cmd.Flags().GetBool("force")
	options.asJSON, _ = cmd.Flags().GetBool("json")
	options.interactive, _ = cmd.Flags().GetBool("interactive")
	options.skipFixture, _ = cmd.Flags().GetBool("skip-fixture")
	options.requireReady, _ = cmd.Flags().GetBool("require-ready")
	if options.workspace == "" {
		options.workspace = "."
	}
	if options.configPath == "" {
		options.configPath = filepath.Join(options.workspace, "codehelper.toml")
	}
	if options.dataDir == "" {
		options.dataDir = filepath.Join(options.workspace, ".codehelper")
	}
	if options.interactive && options.asJSON {
		return setupOptions{}, fmt.Errorf("--interactive cannot be combined with --json")
	}
	if options.interactive {
		if err := promptSetupOptions(bufio.NewReader(stdin), stderr, &options); err != nil {
			return setupOptions{}, err
		}
	}
	return options, nil
}

func promptSetupOptions(
	reader *bufio.Reader,
	writer io.Writer,
	options *setupOptions,
) error {
	providers := model.DefaultCatalog().Providers()
	if len(providers) == 0 {
		return fmt.Errorf("model catalog is empty")
	}
	if options.provider == "" {
		options.provider = providers[0].ID
	}
	value, err := promptValue(reader, writer, "Provider", options.provider)
	if err != nil {
		return err
	}
	options.provider = value
	provider, ok := model.DefaultCatalog().Provider(options.provider)
	if !ok {
		return fmt.Errorf("unknown provider %q", options.provider)
	}
	models := make([]string, 0, len(provider.Models))
	for id := range provider.Models {
		models = append(models, id)
	}
	sort.Strings(models)
	if options.model == "" && len(models) != 0 {
		options.model = models[0]
	}
	if options.model, err = promptValue(reader, writer, "Model", options.model); err != nil {
		return err
	}
	if options.credentialKind == "" {
		options.credentialKind = provider.Credential.Kind
	}
	if options.credentialName == "" {
		options.credentialName = provider.Credential.Name
	}
	if options.credentialKind, err = promptValue(
		reader, writer, "Credential kind", options.credentialKind,
	); err != nil {
		return err
	}
	options.credentialName, err = promptValue(
		reader, writer, "Credential reference", options.credentialName,
	)
	return err
}

func promptValue(
	reader *bufio.Reader,
	writer io.Writer,
	label, fallback string,
) (string, error) {
	_, _ = fmt.Fprintf(writer, "%s [%s]: ", label, fallback)
	value, err := reader.ReadString('\n')
	if err != nil && len(value) == 0 {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return value, nil
}

func resolveSetupSelection(options setupOptions) (setupSelection, error) {
	if options.provider == "" && options.model == "" {
		if options.credentialKind != "" || options.credentialName != "" {
			return setupSelection{}, fmt.Errorf(
				"--credential-kind and --credential-name require --provider and --model",
			)
		}
		if options.probeCapabilities != "" {
			return setupSelection{}, fmt.Errorf(
				"--probe-capabilities requires --provider and --model",
			)
		}
		return setupSelection{}, nil
	}
	if options.provider == "" || options.model == "" {
		return setupSelection{}, fmt.Errorf("--provider and --model must be set together")
	}
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		return setupSelection{}, err
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: options.provider,
		ModelID:    options.model,
	})
	if err != nil {
		return setupSelection{}, err
	}
	credential := route.Credential()
	if options.credentialKind != "" || options.credentialName != "" {
		if options.credentialKind == "" || options.credentialName == "" {
			return setupSelection{}, fmt.Errorf(
				"--credential-kind and --credential-name must be set together",
			)
		}
		credential = model.CredentialRef{
			Kind: options.credentialKind,
			Name: options.credentialName,
		}
	}
	switch credential.Kind {
	case "", "env", "file", "keyring":
	default:
		return setupSelection{}, fmt.Errorf(
			"unsupported credential reference kind %q",
			credential.Kind,
		)
	}
	return setupSelection{
		Provider: options.provider, Model: options.model,
		Protocol:       string(route.Protocol()),
		CredentialKind: credential.Kind,
		CredentialName: credential.Name,
	}, nil
}

func createSetupWorkspace(
	options setupOptions,
) ([]string, constitution.Status, error) {
	if err := os.MkdirAll(options.workspace, 0o700); err != nil {
		return nil, constitution.Status{}, err
	}
	if err := os.MkdirAll(options.dataDir, 0o700); err != nil {
		return nil, constitution.Status{}, err
	}
	if _, err := os.Stat(options.configPath); err == nil && !options.force {
		return nil, constitution.Status{}, fmt.Errorf(
			"%s exists (use --force)",
			options.configPath,
		)
	}
	body, err := config.RenderProfile(options.profile, config.ProfileOptions{
		Workspace: options.workspace,
		DataDir:   options.dataDir,
	})
	if err != nil {
		return nil, constitution.Status{}, err
	}
	if err := os.MkdirAll(filepath.Dir(options.configPath), 0o700); err != nil {
		return nil, constitution.Status{}, err
	}
	if err := os.WriteFile(options.configPath, body, 0o600); err != nil {
		return nil, constitution.Status{}, err
	}
	constitutionPath := filepath.Join(options.dataDir, constitution.FileName)
	if err := constitution.WriteTemplate(constitutionPath, options.force); err != nil {
		return nil, constitution.Status{}, err
	}
	created := []string{options.configPath, options.dataDir, constitutionPath}
	for _, sub := range []string{"fleet", "lanes", "plugins", "skills"} {
		path := filepath.Join(options.dataDir, sub)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, constitution.Status{}, err
		}
		created = append(created, path)
	}
	status, err := constitution.Load(options.workspace, "")
	if err != nil {
		return nil, constitution.Status{}, err
	}
	return created, status.Status, nil
}

func writeSetupSelection(path string, selection setupSelection) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	document := map[string]any{}
	if err := toml.Unmarshal(data, &document); err != nil {
		return err
	}
	execution, _ := document["execution"].(map[string]any)
	if execution == nil {
		execution = map[string]any{}
	}
	execution["provider"] = selection.Provider
	execution["model"] = selection.Model
	execution["protocol"] = selection.Protocol
	document["execution"] = execution
	if selection.CredentialKind != "" {
		document["credential"] = map[string]any{
			"kind": selection.CredentialKind,
			"name": selection.CredentialName,
		}
	}
	encoded, err := toml.Marshal(document)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o600)
}

func setupSelectionChecks(selection setupSelection) []protocol.ReadinessCheck {
	if selection.Provider == "" {
		return []protocol.ReadinessCheck{
			{
				ID: "setup.model", Status: protocol.ReadinessDegraded,
				Reason: "no live provider and model were selected",
				Impact: "only the hermetic fixture path is configured",
				Action: "rerun setup with --provider and --model",
			},
			{
				ID: "setup.credential", Status: protocol.ReadinessDegraded,
				Reason: "no credential reference is configured",
				Impact: "authenticated live providers cannot be used",
				Action: "rerun setup with a non-secret credential reference",
			},
		}
	}
	checks := []protocol.ReadinessCheck{{
		ID: "setup.model", Status: protocol.ReadinessReady,
		Reason: fmt.Sprintf(
			"catalog route %s/%s is configured",
			selection.Provider,
			selection.Model,
		),
	}}
	if selection.CredentialKind == "" {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "setup.credential", Status: protocol.ReadinessReady,
			Reason: "the selected provider does not require a credential reference",
		})
	} else {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "setup.credential", Status: protocol.ReadinessReady,
			Reason: fmt.Sprintf(
				"%s credential reference %s is configured",
				selection.CredentialKind,
				selection.CredentialName,
			),
		})
	}
	return checks
}

func verifySetupFixture(
	ctx context.Context,
	configPath, workspace, fixturePath string,
) error {
	cleanup := func() {}
	if fixturePath == "" {
		var err error
		fixturePath, cleanup, err = materializeSetupFixture()
		if err != nil {
			return err
		}
	}
	defer cleanup()
	dataDir, err := os.MkdirTemp("", "codehelper-setup-state-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dataDir)
	var output, errors bytes.Buffer
	code := runExec(
		ctx,
		[]string{
			"--config", configPath,
			"--provider-fixture", fixturePath,
			"--data-dir", dataDir,
			"--workspace", workspace,
			"--output-format", "stream-json",
			"setup fixture check",
		},
		strings.NewReader(""),
		&output,
		&errors,
	)
	if code != 0 {
		return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(errors.String()))
	}
	if !strings.Contains(output.String(), `"turn.completed"`) {
		return fmt.Errorf("fixture produced no turn.completed event")
	}
	return nil
}

func materializeSetupFixture() (string, func(), error) {
	dir, err := os.MkdirTemp("", "codehelper-setup-fixture-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for _, name := range []string{"fixture.json", "stream.sse"} {
		data, err := bundledSetupFixture.ReadFile("setupfixture/" + name)
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return dir, cleanup, nil
}

func runSetupModelProbe(
	ctx context.Context,
	options setupOptions,
	selection setupSelection,
) ([]wire.ProbeResult, error) {
	capabilities, err := parseProbeCapabilities(options.probeCapabilities)
	if err != nil {
		return nil, err
	}
	store, err := state.Open(ctx, state.Options{DataDir: options.dataDir})
	if err != nil {
		return nil, err
	}
	defer store.Close(ctx)
	return wire.ProbeModelCapabilities(ctx, wire.ProbeOptions{
		ProviderID:   selection.Provider,
		ModelID:      selection.Model,
		Capabilities: capabilities,
		Store:        store,
		Credential: model.CredentialRef{
			Kind: selection.CredentialKind,
			Name: selection.CredentialName,
		},
	})
}

func modelProbeReadiness(results []wire.ProbeResult) protocol.ReadinessCheck {
	for _, result := range results {
		if !result.Supported {
			return protocol.ReadinessCheck{
				ID: "setup.model_probe", Status: protocol.ReadinessDegraded,
				Reason: "the live model probe found unsupported capabilities",
				Impact: "some selected model capabilities will remain disabled",
				Action: "select a compatible model or remove the unsupported capability",
			}
		}
	}
	return protocol.ReadinessCheck{
		ID: "setup.model_probe", Status: protocol.ReadinessReady,
		Reason: "the requested live model capabilities were verified",
	}
}

func writeSetupText(writer io.Writer, report setupReport) {
	_, _ = fmt.Fprintf(
		writer,
		"setup status=%s config=%s data_dir=%s provider=%s model=%s\n",
		report.Status,
		report.Config,
		report.DataDir,
		report.Selection.Provider,
		report.Selection.Model,
	)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s",
			check.ID,
			check.Status,
			check.Reason,
		)
		if check.Action != "" {
			_, _ = fmt.Fprintf(writer, "\taction=%s", check.Action)
		}
		_, _ = fmt.Fprintln(writer)
	}
}
