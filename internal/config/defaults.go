package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Defaults() Config {
	return Config{
		Runtime: Runtime{
			OperationBuffer:  64,
			EventHistory:     256,
			SubscriberBuffer: 64,
		},
		State: State{
			DataDir:        defaultDataDir(),
			BusyTimeout:    5 * time.Second,
			EventRetention: 1_000_000,
		},
		Memory: Memory{Path: defaultMemoryPath()},

		Context: Context{
			Index: Index{Enabled: true, MaxFileBytes: 1 << 20, MaxFiles: 20000},

			RepoMap:    RepoMap{Enabled: true, MaxBytes: 8 << 10, MaxDirectories: 24},
			WorkingSet: WorkingSet{Enabled: true, MaxEntries: 16, MaxBytes: 8 << 10},

			Evidence:     Evidence{Enabled: true, MaxEntries: 24, MaxBytes: 4 << 10},
			CodingPolicy: CodingPolicy{Enabled: true},

			Compact: Compact{
				MaxHistoryBytes: 256 << 10, SummaryMaxBytes: 8 << 10, MaxDigestEntries: 120,
			},
		},
		Telemetry: Telemetry{LogLevel: "info"},
		Execution: Execution{
			Protocol: "openai_chat", Mode: "act", Workspace: ".",
			MaxOutputTokens: 4096, MaxSteps: 256, Timeout: 2 * time.Minute,
			IdleTimeout: 60 * time.Second, MaxConcurrent: 8,

			Verify: Verify{
				Mode: "soft", Scope: "diagnostics", OnFailure: "fail",
				MaxRepairSteps: 1, Timeout: 2 * time.Minute,
			},

			Subagent: Subagent{
				MaxDepth: 5, MaxParallel: 4, MaxSteps: 8,
				WallTime: 5 * time.Minute, Workspace: SubagentWorkspaceAuto,
			},

			Worker: Worker{
				MaxParallel: 2, MaxAttempts: 1, Lease: 30 * time.Second,
				ClaimInterval: time.Second, AutomationInterval: 30 * time.Second,
				RetryBackoff: 15 * time.Second, RetryBackoffMax: 10 * time.Minute,
			},

			Journal: Journal{Durable: true, RecoverOnStart: true},
		},
	}
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".codehelper", "v1")
	}
	return filepath.Join(home, ".codehelper", "v1")
}

func defaultMemoryPath() string {
	return filepath.Join(defaultDataDir(), "memory")
}
