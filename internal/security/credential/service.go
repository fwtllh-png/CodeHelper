package credential

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/keyring"
)

const maxSecretBytes = 32 << 10

type Reference struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Status struct {
	Reference        Reference  `json:"reference"`
	Configured       bool       `json:"configured"`
	Validation       string     `json:"validation"`
	ValidationDetail string     `json:"validation_detail,omitempty"`
	ValidatedAt      *time.Time `json:"validated_at,omitempty"`
	RestartRequired  bool       `json:"restart_required,omitempty"`
}

type Service struct {
	mu              sync.RWMutex
	reference       Reference
	keyring         store
	probe           func(context.Context, Reference) error
	control         *Control
	restartRequired bool
}

type store interface {
	Lookup(context.Context, string) (string, error)
	Set(string, string) error
	Delete(string) error
}

type Option func(*Service)

func WithProbe(probe func(context.Context, Reference) error) Option {
	return func(service *Service) {
		service.probe = probe
	}
}

func WithControl(control *Control) Option {
	return func(service *Service) {
		service.control = control
		if control != nil {
			service.keyring = control.keyring
		}
	}
}

func New(reference Reference, options ...Option) *Service {
	service := &Service{reference: reference, keyring: keyring.New()}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	return s.status(ctx, false)
}

func (s *Service) SetKeyring(
	ctx context.Context,
	secret string,
) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reference.Kind != "keyring" || s.reference.Name == "" {
		if s.control == nil {
			return Status{}, errors.New("Runtime credential is not configured for the OS keyring")
		}
	}
	if strings.TrimSpace(secret) == "" ||
		len(secret) > maxSecretBytes ||
		strings.IndexByte(secret, 0) >= 0 {
		return Status{}, errors.New("credential secret is invalid")
	}
	if s.control != nil {
		reference, err := s.control.rotate(ctx, s.reference, secret)
		if err != nil {
			return Status{}, err
		}
		s.reference = reference
		s.restartRequired = true
		return s.statusFor(ctx, true, reference, true)
	}
	if err := s.keyring.Set(s.reference.Name, secret); err != nil {
		return Status{}, err
	}
	return s.statusFor(ctx, false, s.reference, s.restartRequired)
}

func (s *Service) ClearKeyring(ctx context.Context) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reference.Kind != "keyring" || s.reference.Name == "" {
		return Status{}, errors.New("Runtime credential is not configured for the OS keyring")
	}
	if s.control != nil {
		if err := s.control.clear(ctx, s.reference); err != nil {
			return Status{}, err
		}
		s.reference = Reference{}
		s.restartRequired = true
		return Status{
			Validation: "not_validated", RestartRequired: true,
		}, nil
	}
	if err := s.keyring.Delete(s.reference.Name); err != nil {
		return Status{}, err
	}
	return Status{
		Reference: s.reference, Configured: false, Validation: "not_validated",
	}, nil
}

func (s *Service) Validate(ctx context.Context) (Status, error) {
	return s.status(ctx, true)
}

func (s *Service) status(ctx context.Context, validate bool) (Status, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusFor(
		ctx,
		validate,
		s.reference,
		s.restartRequired,
	)
}

func (s *Service) statusFor(
	ctx context.Context,
	validate bool,
	reference Reference,
	restartRequired bool,
) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	result := Status{
		Reference: reference, Validation: "not_validated",
		RestartRequired: restartRequired,
	}
	switch reference.Kind {
	case "":
		return result, nil
	case "env":
		result.Configured = strings.TrimSpace(os.Getenv(reference.Name)) != ""
	case "keyring":
		value, err := s.keyring.Lookup(ctx, reference.Name)
		if err != nil {
			if errors.Is(err, keyring.ErrNotFound) {
				return result, nil
			}
			return Status{}, err
		}
		result.Configured = strings.TrimSpace(value) != ""
	case "file":
		// File references are resolved by the provider credential resolver. The
		// Web surface deliberately does not widen that resolver's read roots.
		result.Configured = reference.Name != ""
	default:
		return Status{}, errors.New("Runtime credential kind is unsupported")
	}
	if validate {
		now := time.Now().UTC()
		result.ValidatedAt = &now
		if !result.Configured {
			result.Validation = "invalid"
			result.ValidationDetail = "credential is not configured"
		} else if s.probe != nil {
			if err := s.probe(ctx, reference); err != nil {
				result.Validation = "invalid"
				result.ValidationDetail = "provider probe failed"
			} else {
				result.Validation = "valid"
			}
		}
	}
	return result, nil
}
