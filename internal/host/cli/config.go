package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/config"
)

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || !configCommand(args[0]) {
		_, _ = fmt.Fprintln(
			stderr,
			"codehelper: config requires check, show, reload, profile, or explain",
		)
		return 2
	}
	command := args[0]
	flags := flag.NewFlagSet("config "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "TOML configuration file")
	operationBuffer := flags.Int("operation-buffer", 0, "runtime operation queue capacity")
	eventHistory := flags.Int("event-history", 0, "runtime event history capacity")
	subscriberBuffer := flags.Int("subscriber-buffer", 0, "runtime subscriber capacity")
	logLevel := flags.String("log-level", "", "log level")
	credentialKind := flags.String("credential-kind", "", "credential reference kind")
	credentialName := flags.String("credential-name", "", "credential reference name")
	profileName := flags.String("profile", "minimal", "minimal, recommended, or advanced")
	workspace := flags.String("workspace", ".", "workspace used by generated profiles")
	dataDir := flags.String("data-dir", ".codehelper", "state directory used by generated profiles")
	asJSON := flags.Bool("json", false, "emit JSON")
	parseArgs := args[1:]
	explainField := ""
	fieldBeforeFlags := false
	if command == "explain" && len(parseArgs) > 0 &&
		!strings.HasPrefix(parseArgs[0], "-") {
		explainField = parseArgs[0]
		fieldBeforeFlags = true
		parseArgs = parseArgs[1:]
	}
	if err := flags.Parse(parseArgs); err != nil {
		return 2
	}
	if command != "explain" && flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "codehelper: config %s does not accept positional arguments\n", command)
		return 2
	}
	if command == "explain" && explainField == "" && flags.NArg() == 1 {
		explainField = flags.Arg(0)
	}
	if command == "explain" &&
		(explainField == "" || flags.NArg() > 1 ||
			(fieldBeforeFlags && flags.NArg() != 0)) {
		_, _ = fmt.Fprintln(stderr, "codehelper: config explain requires FIELD")
		return 2
	}
	if command == "profile" {
		profile, err := config.ParseProfile(*profileName)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: config profile: %v\n", err)
			return 2
		}
		rendered, err := config.RenderProfile(profile, config.ProfileOptions{
			Workspace: *workspace,
			DataDir:   *dataDir,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: config profile: %v\n", err)
			return 1
		}
		_, _ = stdout.Write(rendered)
		return 0
	}

	overrides := config.Overrides{}
	flags.Visit(func(item *flag.Flag) {
		switch item.Name {
		case "operation-buffer":
			overrides.OperationBuffer = operationBuffer
		case "event-history":
			overrides.EventHistory = eventHistory
		case "subscriber-buffer":
			overrides.SubscriberBuffer = subscriberBuffer
		case "log-level":
			overrides.LogLevel = logLevel
		case "credential-kind":
			overrides.CredentialKind = credentialKind
		case "credential-name":
			overrides.CredentialName = credentialName
		}
	})

	options := config.LoadOptions{Path: *path, Overrides: overrides}
	if command == "reload" {
		manager, err := config.NewManager(config.LoadOptions{
			LookupEnv: func(string) (string, bool) { return "", false },
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: initialize config manager: %v\n", err)
			return 1
		}
		event := manager.ReloadFrom(options)
		if err := encodeJSON(stdout, event); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: encode config reload event: %v\n", err)
			return 1
		}
		if event.Problem != nil {
			return 1
		}
		return 0
	}

	snapshot, err := config.Load(options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: config %s: %v\n", command, err)
		return 1
	}
	if command == "check" {
		_, _ = fmt.Fprintln(stdout, "configuration is valid")
		return 0
	}
	if command == "explain" {
		explanation, err := snapshot.Explain(explainField)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: config explain: %v\n", err)
			return 2
		}
		if *asJSON {
			if err := encodeJSON(stdout, explanation); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: config explain: %v\n", err)
				return 1
			}
		} else {
			_, _ = fmt.Fprintf(
				stdout,
				"%s current=%v default=%v source=%s risk=%s impact=%s\n",
				explanation.Field,
				explanation.Current,
				explanation.Default,
				explanation.Source,
				explanation.Risk,
				explanation.Impact,
			)
		}
		return 0
	}
	if err := encodeJSON(stdout, snapshot); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: encode config: %v\n", err)
		return 1
	}
	return 0
}

func configCommand(value string) bool {
	switch value {
	case "check", "show", "reload", "profile", "explain":
		return true
	default:
		return false
	}
}

func encodeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
