package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func runRuntimeObserve(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("runtime-observe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	eventCount := flags.Int("events", 10, "number of operations")
	configPath := flags.String("config", "", "TOML configuration file")
	logPath := flags.String("log-file", "", "JSON log output file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *eventCount <= 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: runtime-observe requires --events with a positive value")
		return 2
	}
	snapshot, err := config.Load(config.LoadOptions{Path: *configPath})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: runtime-observe config: %v\n", err)
		return 1
	}

	logWriter := stderr
	var logFile *os.File
	if *logPath != "" {
		logFile, err = os.OpenFile(*logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: open runtime log: %v\n", err)
			return 1
		}
		defer logFile.Close()
		logWriter = logFile
	}
	var secrets []string
	if reference := snapshot.Config.Credential; reference.Kind == "env" {
		if value, exists := os.LookupEnv(reference.Name); exists {
			secrets = append(secrets, value)
		}
	}
	logger := telemetry.NewJSONLogger(
		logWriter,
		parseSlogLevel(snapshot.Config.Telemetry.LogLevel),
		telemetry.NewRedactor(secrets...),
	)
	metrics := telemetry.NewMetrics()
	runtime := app.NewRuntime(app.Options{
		OperationBuffer:  snapshot.Config.Runtime.OperationBuffer,
		EventHistory:     snapshot.Config.Runtime.EventHistory,
		SubscriberBuffer: snapshot.Config.Runtime.SubscriberBuffer,
		Engine:           app.NoopEngine{},
		Metrics:          metrics,
		Logger:           logger,
	})
	eventStream, err := runtime.Events(context.Background(), 0)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: subscribe to observed runtime: %v\n", err)
		return 1
	}
	for index := range *eventCount {
		operation, createErr := protocol.NewOperation(&protocol.StartTurnPayload{
			ThreadID: protocol.ThreadID(fmt.Sprintf("observe_thread_%d", index)),
			TurnID:   protocol.TurnID(fmt.Sprintf("observe_turn_%d", index)),
			ItemID:   protocol.ItemID(fmt.Sprintf("observe_item_%d", index)),
			Prompt:   "observe",
		})
		if createErr != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: create observed operation: %v\n", createErr)
			return 1
		}
		if submitErr := submitObserved(runtime, operation); submitErr != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: submit observed operation: %v\n", submitErr)
			return 1
		}
	}
	terminals := 0
	for terminals < *eventCount {
		select {
		case event, open := <-eventStream:
			if !open {
				_, _ = fmt.Fprintln(stderr, "codehelper: observed runtime stream closed early")
				return 1
			}
			if protocol.IsTerminalEvent(event.Kind) {
				terminals++
			}
		case <-time.After(5 * time.Second):
			_, _ = fmt.Fprintln(stderr, "codehelper: observed runtime timed out")
			return 1
		}
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Close(closeCtx); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: close observed runtime: %v\n", err)
		return 1
	}
	if err := encodeJSON(stdout, runtime.Snapshot(context.Background())); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: encode runtime metrics: %v\n", err)
		return 1
	}
	return 0
}

func submitObserved(runtime *app.Runtime, operation protocol.Operation) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := runtime.Submit(context.Background(), operation)
		if err == nil {
			return nil
		}
		if err != app.ErrQueueFull || time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}

func parseSlogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
