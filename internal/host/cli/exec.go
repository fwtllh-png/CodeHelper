package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/eventview"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func runExec(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("exec", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "TOML configuration file")
	dataDir := flags.String("data-dir", "", "CodeHelper extension/session state directory")
	providerID := flags.String("provider", "", "explicit provider id")
	modelID := flags.String("model", "", "model id within the selected provider")
	baseURL := flags.String("base-url", "", "custom provider base URL")
	protocolName := flags.String("protocol", "openai_chat", "wire protocol")
	apiKeyEnv := flags.String("api-key-env", "", "environment variable containing the provider credential")
	fixturePath := flags.String("provider-fixture", "", "directory containing an HTTP provider fixture")
	outputFormat := flags.String("output-format", "stream-json", "output format")
	maxOutputTokens := flags.Uint64("max-output-tokens", 4096, "maximum output tokens")
	budgetTokens := flags.Uint64("budget-tokens", 0, "maximum projected cumulative tokens")
	budgetUSD := flags.Float64("budget-usd", 0, "maximum projected cumulative cost")
	reasoningEffort := flags.String("reasoning-effort", "", "provider reasoning effort")
	nativeSearch := flags.Bool("native-search", false, "enable provider-native search")
	lockRoute := flags.Bool(
		"lock-route",
		false,
		"refuse to fall back to the act model when a purpose has no [route.*] slot",
	)
	trustProbe := flags.Bool(
		"trust-probe",
		false,
		"let capability probe observations that say supported widen catalog capabilities",
	)
	enableTools := flags.Bool("enable-tools", false, "enable built-in workspace tools")
	workspace := flags.String("workspace", ".", "workspace used by built-in tools")
	modeName := flags.String("mode", "act", "tool mode: plan, act, or operate")
	var postureName string
	flags.StringVar(&postureName, "posture", "auto", "tool permission posture: suggest, auto, bypass, or never")
	flags.StringVar(&postureName, "permission", "auto", "alias for --posture")
	repositoryRulesPath := flags.String("repository-rules", "", "JSON repository policy rules")
	approvalStdin := flags.Bool(
		"approval-stdin",
		false,
		"read approval decisions as NDJSON from stdin",
	)
	revertAfterComplete := flags.Bool(
		"revert-after-complete",
		false,
		"submit a turn.revert operation after a successful turn",
	)
	pluginBundle := flags.String("plugin-bundle", "", "reviewed plugin bundle path within workspace")
	pluginReceipt := flags.String("plugin-receipt", "", "trusted plugin receipt JSON path")
	mcpConfig := flags.String("mcp-config", "", "versioned MCP stdio server config JSON")
	metricsPath := flags.String("metrics-file", "", "write a JSON metric snapshot")
	logPath := flags.String("log-file", "", "write redacted JSON logs")
	modelMetadataPath := flags.String("model-metadata", "", "JSON file with explicit custom model metadata")
	contextTokens := flags.Uint64("context-tokens", 0, "custom model context window")
	modelMaxOutputTokens := flags.Uint64("model-max-output-tokens", 0, "custom model output limit")
	modelCapabilities := flags.String("model-capabilities", "", "comma-separated custom model capabilities")
	inputPrice := flags.Float64("input-price-per-million", 0, "custom model input price")
	outputPrice := flags.Float64("output-price-per-million", 0, "custom model output price")
	pricingCurrency := flags.String("pricing-currency", "", "custom model pricing currency")
	maxSteps := flags.Int("max-steps", 1, "maximum model steps for a turn")
	timeout := flags.Duration("timeout", 2*time.Minute, "overall provider request timeout")
	idleTimeout := flags.Duration("stream-idle-timeout", time.Minute, "maximum stream idle duration")
	maxConcurrent := flags.Int("provider-concurrency", 8, "maximum concurrent provider streams")
	rateLimit := flags.Float64("provider-rate-limit", 0, "provider requests per second; zero disables")
	resume := flags.Bool("resume", false, "resume the active thread under --data-dir")
	continueSession := flags.Bool("continue", false, "alias for --resume (continue last session thread)")
	sessionIDFlag := flags.String("session-id", "", "explicit session id recorded with the turn")
	threadIDFlag := flags.String("thread-id", "", "explicit thread id (overrides --resume lookup)")
	attachImage := flags.String("attach-image", "", "workspace-relative image path(s) for image_analyze; repeat via comma-separated list (max 3)")
	var files repeatedPaths
	flags.Var(&files, "file", "workspace-relative file to put in the working set; repeatable")
	extensionFlags := addExtensionFlags(flags)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	visited := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { visited[item.Name] = true })
	if flags.NArg() == 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: exec requires a prompt")
		return 2
	}
	if *outputFormat != "stream-json" {
		_, _ = fmt.Fprintln(stderr, "codehelper: exec currently supports only --output-format stream-json")
		return 2
	}
	if err := validateExecFlags(
		*protocolName, *modeName, postureName, *enableTools,
		*repositoryRulesPath, *pluginBundle, *pluginReceipt,
	); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: exec: %v\n", err)
		return 2
	}

	threadID, sessionID, err := resolveExecSession(*dataDir, *threadIDFlag, *sessionIDFlag, *resume || *continueSession)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: exec: %v\n", err)
		return 2
	}
	var resumedSnap *ux.Snapshot
	if (*resume || *continueSession) && *dataDir != "" {
		if snap, snapErr := ux.LoadSnapshot(*dataDir, sessionID); snapErr == nil {
			resumedSnap = &snap
			if !visited["provider"] && snap.Provider != "" {
				*providerID = snap.Provider
				visited["provider"] = true
			}
			if !visited["model"] && snap.Model != "" {
				*modelID = snap.Model
				visited["model"] = true
			}
			if !visited["mode"] && snap.Mode != "" {
				*modeName = snap.Mode
				visited["mode"] = true
			}
			if !visited["workspace"] && snap.Workspace != "" {
				*workspace = snap.Workspace
				visited["workspace"] = true
			}
		}
	}

	overrides := config.Overrides{
		Provider:        pointerIfVisited(visited, "provider", providerID),
		Model:           pointerIfVisited(visited, "model", modelID),
		Protocol:        pointerIfVisited(visited, "protocol", protocolName),
		Mode:            pointerIfVisited(visited, "mode", modeName),
		Workspace:       pointerIfVisited(visited, "workspace", workspace),
		Tools:           pointerIfVisited(visited, "enable-tools", enableTools),
		MaxOutputTokens: pointerIfVisited(visited, "max-output-tokens", maxOutputTokens),
		MaxSteps:        pointerIfVisited(visited, "max-steps", maxSteps),
		Timeout:         pointerIfVisited(visited, "timeout", timeout),
		IdleTimeout:     pointerIfVisited(visited, "stream-idle-timeout", idleTimeout),
		MaxConcurrent:   pointerIfVisited(visited, "provider-concurrency", maxConcurrent),
		RateLimit:       pointerIfVisited(visited, "provider-rate-limit", rateLimit),
		BudgetTokens:    pointerIfVisited(visited, "budget-tokens", budgetTokens),
		BudgetUSD:       pointerIfVisited(visited, "budget-usd", budgetUSD),
		ReasoningEffort: pointerIfVisited(visited, "reasoning-effort", reasoningEffort),
		NativeSearch:    pointerIfVisited(visited, "native-search", nativeSearch),
		RouteLock:       pointerIfVisited(visited, "lock-route", lockRoute),
	}
	if *dataDir != "" {
		overrides.StateDataDir = dataDir
	}

	var store *state.Store
	if *dataDir != "" {
		loaded, err := config.Load(config.LoadOptions{
			Path: *configPath, Overrides: overrides,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: exec config: %v\n", err)
			return 1
		}
		store, err = state.Open(ctx, state.Options{
			DataDir: *dataDir, BusyTimeout: loaded.Config.State.BusyTimeout,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: exec state: %v\n", err)
			return 1
		}
		defer func() {
			_ = store.CloseAll(context.Background())
		}()
		workspaceRoot := loaded.Config.Execution.Workspace
		if workspaceRoot == "" {
			workspaceRoot = *workspace
		}
		if err := apppersistence.EnsureThread(
			ctx, store, threadID, sessionID, workspaceRoot,
		); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: exec ensure thread: %v\n", err)
			return 1
		}
	}

	workingSet, err := resolveWorkingSet(files, *workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: exec --file: %v\n", err)
		return 2
	}

	// The Runtime owns an independent lifecycle so a host cancellation can be
	// translated into a typed turn.cancel operation and observed as an event
	// before the application is closed.
	session, err := wire.NewExec(context.Background(), wire.ExecOptions{
		WorkingSet: workingSet,
		ConfigPath: *configPath, ConfigOverrides: overrides,
		BaseURL: *baseURL, APIKeyEnv: *apiKeyEnv, FixturePath: *fixturePath,
		Permission: postureName, RepositoryRulesPath: *repositoryRulesPath,
		PluginBundle: *pluginBundle, PluginReceipt: *pluginReceipt,
		MCPConfigPath: *mcpConfig,
		MetricsPath:   *metricsPath, LogPath: *logPath,
		PersistentStore: store,
		TrustProbe:      *trustProbe,
		Extensions:      extensionFlags.options(*dataDir),
		ModelMetadata: wire.ModelMetadataOptions{
			Path: *modelMetadataPath, ContextTokens: *contextTokens,
			MaxOutputTokens: *modelMaxOutputTokens, Capabilities: *modelCapabilities,
			InputPerMillion: *inputPrice, OutputPerMillion: *outputPrice,
			Currency: *pricingCurrency, ContextSet: visited["context-tokens"],
			OutputSet:       visited["model-max-output-tokens"],
			CapabilitiesSet: visited["model-capabilities"],
			InputPriceSet:   visited["input-price-per-million"],
			OutputPriceSet:  visited["output-price-per-million"],
			CurrencySet:     visited["pricing-currency"],
		},
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: exec setup: %v\n", err)
		return 1
	}
	// Undoing someone's half-applied edits is not something to do silently, even
	// when undoing them is the right call.
	if recovery := session.JournalRecovery(); !recovery.Empty() {
		_, _ = fmt.Fprintf(
			stderr,
			"codehelper: recovered interrupted turns: %d rolled back, %d committed kept, %d left to a live process\n",
			len(recovery.RolledBack), len(recovery.Abandoned), len(recovery.Skipped),
		)
	}
	prompt := strings.Join(flags.Args(), " ")
	if rawImages := strings.TrimSpace(*attachImage); rawImages != "" {
		parts := strings.Split(rawImages, ",")
		paths := make([]string, 0, len(parts))
		for _, part := range parts {
			imagePath := strings.TrimSpace(part)
			if imagePath == "" {
				continue
			}
			if filepath.IsAbs(imagePath) {
				_, _ = fmt.Fprintln(stderr, "codehelper: exec --attach-image must be workspace-relative")
				return 2
			}
			cleaned := filepath.Clean(imagePath)
			if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
				_, _ = fmt.Fprintln(stderr, "codehelper: exec --attach-image escapes workspace")
				return 2
			}
			abs := filepath.Join(*workspace, cleaned)
			if info, err := os.Stat(abs); err != nil || info.IsDir() {
				_, _ = fmt.Fprintf(stderr, "codehelper: exec --attach-image: %v\n", err)
				return 2
			}
			paths = append(paths, cleaned)
		}
		if len(paths) > 3 {
			_, _ = fmt.Fprintln(stderr, "codehelper: exec --attach-image supports at most 3 images")
			return 2
		}
		if len(paths) > 0 {
			prompt = fmt.Sprintf(
				"[attached image paths=%s — use image_analyze once per workspace-relative path when vision is configured]\n\n%s",
				strings.Join(paths, ", "), prompt,
			)
		}
	}
	completedTurnID, runErr := runRuntimeTurn(
		ctx,
		stdin,
		stdout,
		session.Runtime,
		prompt,
		threadID,
		sessionID,
		*approvalStdin,
		*revertAfterComplete,
		strings.EqualFold(postureName, "bypass"),
	)
	if runErr == nil && *dataDir != "" {
		_ = persistExecSession(*dataDir, threadID, sessionID)
		messages := []string{}
		if resumedSnap != nil {
			messages = append(messages, resumedSnap.Messages...)
		}
		messages = append(messages, prompt)
		if len(messages) > 32 {
			messages = messages[len(messages)-32:]
		}
		_ = ux.SaveSnapshot(*dataDir, ux.Snapshot{
			SessionID: sessionID, ThreadID: string(threadID),
			Provider: *providerID, Model: *modelID, Workspace: *workspace,
			Mode: *modeName, Messages: messages, LastPrompt: prompt,
			TurnCount: len(messages), UpdatedAt: time.Now().UTC(),
		})
		_ = ux.SaveCheckpoint(*dataDir, ux.Checkpoint{
			ThreadID: string(threadID), SessionID: sessionID,
			TurnID: string(completedTurnID), Prompt: prompt, Status: "completed",
		})
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := session.Close(closeCtx)
	cancel()
	if runErr != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: exec failed (%s): %v\n", protocol.CodeOf(runErr), runErr)
		return 1
	}
	if closeErr != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: close exec application: %v\n", closeErr)
		return 1
	}
	return 0
}

func validateExecFlags(
	_, _, permission string,
	enableTools bool,
	repositoryRulesPath, pluginBundle, pluginReceipt string,
) error {
	if !oneOf(permission, "suggest", "auto", "bypass", "never") {
		return errors.New("--posture must be suggest, auto, bypass, or never")
	}
	if (pluginBundle == "") != (pluginReceipt == "") {
		return errors.New("--plugin-bundle and --plugin-receipt must be used together")
	}
	return nil
}

// repeatedPaths collects a flag given more than once, so --file can name several
// files without inventing a separator that a path could legitimately contain.
type repeatedPaths []string

func (p *repeatedPaths) String() string { return strings.Join(*p, ", ") }

func (p *repeatedPaths) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("path must not be empty")
	}
	*p = append(*p, value)
	return nil
}

// resolveWorkingSet turns --file paths into the session's initial working set.
//
// A file the user named by hand is pinned as critical: it is the task's subject,
// so it must not decay out of the working set as the session wanders.
func resolveWorkingSet(paths []string, workspace string) ([]wire.ContextFile, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(paths))
	files := make([]wire.ContextFile, 0, len(paths))
	for _, path := range paths {
		if filepath.IsAbs(path) {
			return nil, fmt.Errorf("%s must be workspace-relative", path)
		}
		cleaned := filepath.Clean(path)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s escapes the workspace", path)
		}
		if _, found := seen[cleaned]; found {
			continue
		}
		seen[cleaned] = struct{}{}
		info, err := os.Stat(filepath.Join(workspace, cleaned))
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory", path)
		}
		files = append(files, wire.ContextFile{Path: cleaned, Critical: true})
	}
	return files, nil
}

func pointerIfVisited[T any](visited map[string]bool, name string, value *T) *T {
	if !visited[name] {
		return nil
	}
	copy := *value
	return &copy
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func runRuntimeTurn(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	runtime *app.Runtime,
	prompt string,
	threadID protocol.ThreadID,
	sessionID string,
	approvalStdin bool,
	revertAfterComplete bool,
	autoAllowApprovals bool,
) (protocol.TurnID, error) {
	if threadID == "" {
		generated, err := protocol.NewThreadID()
		if err != nil {
			return "", err
		}
		threadID = generated
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return "", err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return "", err
	}
	_ = sessionID // recorded via persistExecSession; Runtime StartTurn is thread-scoped.
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: prompt,
	})
	if err != nil {
		return "", err
	}
	// Subscribe from the live tip so a trimmed event ring does not reject cursor 0.
	cursor := runtime.Snapshot(context.Background()).LastSequence
	events, err := runtime.Events(context.Background(), cursor)
	if err != nil {
		var gap *app.CursorGapError
		if errors.As(err, &gap) {
			events, err = runtime.Events(context.Background(), gap.Latest)
		}
	}
	if err != nil {
		return "", err
	}
	if err := runtime.Submit(ctx, operation); err != nil {
		return "", err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	approvalDecoder := json.NewDecoder(stdin)
	cancelContext := ctx.Done()
	waitingForRevert := false
	for {
		select {
		case event, open := <-events:
			if !open {
				return "", errors.New("runtime event stream closed before terminal event")
			}
			if event.TurnID != turnID {
				continue
			}
			if err := encoder.Encode(event); err != nil {
				return "", err
			}
			update, err := eventview.Project(event)
			if err != nil {
				return "", err
			}
			switch data := update.(type) {
			case eventview.InteractionUpdate:
				request := data.ApprovalRequired
				if request == nil {
					continue
				}
				// Headless exec defaults to deny unless --approval-stdin feeds
				// decisions. Bypass/Full already opted out of asks — auto-allow
				// so mid-flight network grants (redirect hosts) are not silently
				// dropped when the operator cannot type into stdin.
				decisionValue := protocol.ApprovalDeny
				scope := protocol.ApprovalScopeOnce
				if !approvalStdin && autoAllowApprovals {
					decisionValue = protocol.ApprovalApprove
					scope = protocol.ApprovalScopeSession
				}
				var replacement json.RawMessage
				if approvalStdin {
					var input struct {
						Decision             protocol.ApprovalDecision `json:"decision"`
						Scope                protocol.ApprovalScope    `json:"scope,omitempty"`
						ReplacementArguments json.RawMessage           `json:"replacement_arguments,omitempty"`
					}
					if err := approvalDecoder.Decode(&input); err != nil {
						return "", fmt.Errorf("read approval decision from stdin: %w", err)
					}
					decisionValue = input.Decision
					if input.Scope != "" {
						scope = input.Scope
					}
					replacement = input.ReplacementArguments
				}
				decisionItemID, err := protocol.NewItemID()
				if err != nil {
					return "", err
				}
				planID := ""
				if request.EditPlan != nil {
					planID = request.EditPlan.ID
				}
				decision, err := protocol.NewOperation(&protocol.ApprovalDecisionPayload{
					ThreadID: threadID, TurnID: turnID, ItemID: decisionItemID,
					RequestID: request.RequestID, Decision: decisionValue,
					Scope: scope, ExpiresAt: request.ExpiresAt,
					ReplacementArguments: replacement,
					PlanID:               planID,
				})
				if err != nil {
					return "", err
				}
				if err := runtime.Submit(ctx, decision); err != nil {
					return "", err
				}
			case eventview.TerminalUpdate:
				switch data.Status {
				case "completed":
					if !revertAfterComplete {
						return turnID, nil
					}
					revertItemID, err := protocol.NewItemID()
					if err != nil {
						return "", err
					}
					revert, err := protocol.NewOperation(&protocol.RevertTurnPayload{
						ThreadID: threadID, TurnID: turnID,
						ItemID: revertItemID, TargetTurnID: turnID,
					})
					if err != nil {
						return "", err
					}
					if err := runtime.Submit(context.Background(), revert); err != nil {
						return "", err
					}
					waitingForRevert = true
				case "failed", "canceled", "rejected":
					return "", protocol.NewProblem(data.Code, data.Message, false, nil)
				}
			case eventview.LifecycleUpdate:
				if data.TurnReverted != nil && waitingForRevert {
					return turnID, nil
				}
			}
		case <-cancelContext:
			cancelContext = nil
			cancelItemID, err := protocol.NewItemID()
			if err != nil {
				return "", err
			}
			cancelOperation, err := protocol.NewOperation(&protocol.CancelTurnPayload{
				ThreadID: threadID,
				TurnID:   turnID,
				ItemID:   cancelItemID,
				Reason:   protocol.CancelReasonHostInterrupted,
			})
			if err != nil {
				return "", err
			}
			submitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			err = runtime.Submit(submitCtx, cancelOperation)
			cancel()
			if err != nil {
				return "", fmt.Errorf("submit cancellation after interrupt: %w", err)
			}
		}
	}
}
