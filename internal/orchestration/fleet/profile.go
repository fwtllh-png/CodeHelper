package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// Profile describes fleet worker concurrency and lease/heartbeat policy.
type Profile struct {
	Name           string            `toml:"name" json:"name"`
	MaxWorkers     int               `toml:"max_workers" json:"max_workers"`
	LeaseTimeout   string            `toml:"lease_timeout" json:"lease_timeout"`
	HeartbeatAlert string            `toml:"heartbeat_alert" json:"heartbeat_alert"`
	Roles          map[string]string `toml:"roles" json:"roles,omitempty"`
}

// DefaultProfile returns a safe offline default.
func DefaultProfile() Profile {
	return Profile{
		Name: "default", MaxWorkers: 2,
		LeaseTimeout: "2m", HeartbeatAlert: "2m",
		Roles: map[string]string{"worker": "default"},
	}
}

// LeaseTimeoutDuration parses LeaseTimeout or falls back to 2m.
func (p Profile) LeaseTimeoutDuration() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(p.LeaseTimeout)); err == nil && d > 0 {
		return d
	}
	return 2 * time.Minute
}

// HeartbeatAlertDuration parses HeartbeatAlert or falls back to lease timeout.
func (p Profile) HeartbeatAlertDuration() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(p.HeartbeatAlert)); err == nil && d > 0 {
		return d
	}
	return p.LeaseTimeoutDuration()
}

// LoadProfile reads a TOML fleet profile from path.
func LoadProfile(path string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	profile := DefaultProfile()
	if err := toml.Unmarshal(data, &profile); err != nil {
		return Profile{}, fmt.Errorf("decode fleet profile: %w", err)
	}
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if profile.MaxWorkers <= 0 {
		profile.MaxWorkers = 1
	}
	return profile, nil
}

// LoadRoster loads name.toml from dir (default name when empty).
func LoadRoster(dir, name string) (Profile, error) {
	if strings.TrimSpace(name) == "" {
		name = "default"
	}
	path := filepath.Join(dir, name+".toml")
	return LoadProfile(path)
}

// WriteTemplate writes a minimal profile template if missing.
func WriteTemplate(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := []byte(`name = "default"
max_workers = 2
lease_timeout = "2m"
heartbeat_alert = "2m"

[roles]
worker = "default"
`)
	return os.WriteFile(path, body, 0o600)
}
