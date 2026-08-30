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
		Memory: Memory{
			Path: defaultMemoryPath(), MaxCandidates: 32,
			MaxPromptBytes: 16 << 10,
		},

		Context: Context{
			Index: Index{Enabled: true, MaxFileBytes: 1 << 20, MaxFiles: 20000},

			RepoMap:    RepoMap{Enabled: true, MaxBytes: 8 << 10, MaxDirectories: 24},
			WorkingSet: WorkingSet{Enabled: true, MaxEntries: 16, MaxBytes: 8 << 10},

			Evidence:     Evidence{Enabled: true, MaxEntries: 24, MaxBytes: 4 << 10},
			CodingPolicy: CodingPolicy{Enabled: true},

			Compact: Compact{
				Scope: "total", SummaryMaxBytes: 0, MaxDigestEntries: 120,
				TruthMaxBytes: 0, TruthMaxEntities: 256,
				MandatoryMaxEntities: 128, FactMaxEntities: 96,
				VerifiedChangeRetentionTurns:     32,
				FailureMaxEntities:               24,
				HandleMaxEntities:                32,
				OmissionSampleMaxEntities:        8,
				RecentTailTurns:                  2,
				RecentTailMaxTokens:              0,
				SemanticNarrative:                "off",
				SemanticNarrativeMaxInputTokens:  4096,
				SemanticNarrativeMaxOutputTokens: 512,
				SemanticNarrativeMaxItems:        32,
				SemanticNarrativeItemMaxBytes:    512,
				SemanticNarrativeTimeout:         30 * time.Second,
				SemanticNarrativeRetryLimit:      1,
				OwnerDeltaMaxSegments:            16,
				OwnerDeltaMaxBytes:               64 << 10,
			},
		},
		Telemetry: Telemetry{LogLevel: "info"},
		Execution: Execution{
			Protocol: "openai_chat", Mode: "act", Workspace: ".",
			MaxSteps:     64,
			Timeout:      2 * time.Minute,
			LeaseTimeout: 2 * time.Minute,
			IdleTimeout:  60 * time.Second, MaxConcurrent: 8,
			ProviderRetryLimit: 3,

			Verify: Verify{
				Mode: "soft", Scope: "diagnostics", OnFailure: "fail",
				MaxRepairSteps: 1, Timeout: 2 * time.Minute,
			},

			Subagent: Subagent{
				Delegation: SubagentDelegationExplicit,
				MaxDepth:   5, MaxParallel: 4, MaxResident: 8, MaxTotal: 16,
				Workspace: SubagentWorkspaceAuto,
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
