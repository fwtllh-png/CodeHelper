package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	LegacyConfigVersion = 1
	ConfigVersion       = 2
)

type Source string

const (
	SourceRepository Source = "repository"
	SourceBuiltin    Source = "builtin"
)

type Trust string

const (
	TrustWorkspace Trust = "workspace"
	TrustSigned    Trust = "signed_registry"
	TrustBuiltin   Trust = "builtin"
)

type Scope string

const (
	ScopeProcess Scope = "process"
	ScopeSession Scope = "session"
	ScopeThread  Scope = "thread"
	ScopeTurn    Scope = "turn"
)

type Mode string

const (
	ModeObserve Mode = "observe"
	ModeEnforce Mode = "enforce"
)

const (
	defaultTimeout        = 30 * time.Second
	defaultMaxOutputBytes = 64 << 10
)

// Config is deliberately versioned independently from the application config
// so hook process contracts can evolve without silently changing policy.
type Config struct {
	Version int                    `json:"version"`
	Hooks   map[Event][]HookConfig `json:"hooks"`
}

type HookConfig struct {
	ID               string                      `json:"id"`
	Source           Source                      `json:"source,omitempty"`
	Trust            Trust                       `json:"trust,omitempty"`
	Scope            Scope                       `json:"scope,omitempty"`
	Mode             Mode                        `json:"mode,omitempty"`
	Command          string                      `json:"command"`
	Args             []string                    `json:"args,omitempty"`
	Env              []string                    `json:"env,omitempty"`
	WorkingDirectory string                      `json:"working_directory,omitempty"`
	ContinueOnError  bool                        `json:"continue_on_error,omitempty"`
	TimeoutText      string                      `json:"timeout,omitempty"`
	Timeout          time.Duration               `json:"-"`
	MaxOutputBytes   int                         `json:"max_output_bytes,omitempty"`
	Authority        func(context.Context) error `json:"-"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return DecodeConfig(data)
}

// LoadRepositoryConfig admits one operator-selected regular config file and
// replaces all self-reported source metadata with repository trust.
func LoadRepositoryConfig(path string) (Config, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Config{}, errors.New(
			"hooks config must be a regular non-symlink file",
		)
	}
	config, err := LoadConfig(path)
	if err != nil {
		return Config{}, err
	}
	BindRepository(&config)
	return config, nil
}

func DecodeConfig(data []byte) (Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode hooks config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return Config{}, errors.New("decode hooks config: trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode hooks config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c *Config) Validate() error {
	if c.Version != LegacyConfigVersion && c.Version != ConfigVersion {
		return fmt.Errorf("unsupported hooks config version %d", c.Version)
	}
	if c.Hooks == nil {
		c.Hooks = make(map[Event][]HookConfig)
	}
	ids := make(map[string]Event)
	for event, configured := range c.Hooks {
		if _, ok := validEvents[event]; !ok {
			return fmt.Errorf("unsupported hook event %q", event)
		}
		for index := range configured {
			hook := &configured[index]
			if hook.Source == "" {
				hook.Source = SourceRepository
			}
			if hook.Trust == "" {
				hook.Trust = TrustWorkspace
			}
			if hook.Scope == "" {
				hook.Scope = ScopeProcess
			}
			if hook.Mode == "" {
				hook.Mode = ModeEnforce
			}
			if !validHookMetadata(*hook) {
				return fmt.Errorf("hook %q: source, trust, scope, or mode is invalid", hook.ID)
			}
			if strings.TrimSpace(hook.ID) == "" {
				return fmt.Errorf("hook %s[%d]: id is required", event, index)
			}
			if previous, exists := ids[hook.ID]; exists {
				return fmt.Errorf("hook id %q is duplicated in %s and %s", hook.ID, previous, event)
			}
			ids[hook.ID] = event
			if strings.TrimSpace(hook.Command) == "" {
				return fmt.Errorf("hook %q: command is required", hook.ID)
			}
			if strings.IndexByte(hook.Command, 0) >= 0 {
				return fmt.Errorf("hook %q: command contains NUL", hook.ID)
			}
			for _, argument := range hook.Args {
				if strings.IndexByte(argument, 0) >= 0 {
					return fmt.Errorf("hook %q: argument contains NUL", hook.ID)
				}
			}
			if hook.TimeoutText != "" {
				timeout, err := time.ParseDuration(hook.TimeoutText)
				if err != nil || timeout <= 0 {
					return fmt.Errorf("hook %q: timeout must be a positive duration", hook.ID)
				}
				hook.Timeout = timeout
			}
			if hook.Timeout < 0 {
				return fmt.Errorf("hook %q: timeout must be positive", hook.ID)
			}
			if hook.MaxOutputBytes < 0 {
				return fmt.Errorf("hook %q: max_output_bytes must be positive", hook.ID)
			}
		}
		c.Hooks[event] = configured
	}
	c.Version = ConfigVersion
	return nil
}

// BindRepository replaces self-reported source and trust metadata on hooks
// loaded from an operator-selected repository configuration.
func BindRepository(config *Config) {
	if config == nil {
		return
	}
	for event, configured := range config.Hooks {
		for index := range configured {
			configured[index].Source = SourceRepository
			configured[index].Trust = TrustWorkspace
		}
		config.Hooks[event] = configured
	}
}

func validHookMetadata(hook HookConfig) bool {
	switch hook.Source {
	case SourceRepository, SourceBuiltin:
	default:
		return false
	}
	switch hook.Trust {
	case TrustWorkspace, TrustSigned, TrustBuiltin:
	default:
		return false
	}
	switch hook.Scope {
	case ScopeProcess, ScopeSession, ScopeThread, ScopeTurn:
	default:
		return false
	}
	return hook.Mode == ModeObserve || hook.Mode == ModeEnforce
}
