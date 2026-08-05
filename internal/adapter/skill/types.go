package skill

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

const (
	DefaultMaxDepth     = 4
	DefaultMaxFileBytes = 1 << 20
	DefaultMaxSkills    = 1024
	DefaultMaxEntries   = 8192
	DefaultMaxResolved  = 256
	DefaultMaxLoadBytes = 4 << 20
)

type Source string

const (
	SourceWorkspace  Source = "workspace"
	SourceConfigured Source = "configured"
	SourceUser       Source = "user"
	SourcePlugin     Source = "plugin"
)

type Metadata struct {
	Name                   string            `json:"name"`
	Description            string            `json:"description"`
	LocalizedDescriptions  map[string]string `json:"localized_descriptions,omitempty"`
	DisableModelInvocation bool              `json:"disable_model_invocation,omitempty"`
	UserInvocable          *bool             `json:"user_invocable,omitempty"`
}

func (m Metadata) DescriptionFor(locale string) string {
	if value := localizedValue(m.LocalizedDescriptions, locale); value != "" {
		return value
	}
	return m.Description
}

type Summary struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Source        Source `json:"source"`
	Path          string `json:"path"`
	Plugin        string `json:"plugin,omitempty"`
	Version       string `json:"version"`
	Compatibility string `json:"compatibility,omitempty"`
	Digest        string `json:"digest"`
	Locked        bool   `json:"locked"`
}

type Loaded struct {
	Summary
	Content      string            `json:"content"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

type Issue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Limits struct {
	MaxDepth     int
	MaxFileBytes int64
	MaxSkills    int
	MaxEntries   int
	MaxResolved  int
	MaxLoadBytes int64
}

func (l Limits) normalized() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.MaxSkills <= 0 {
		l.MaxSkills = DefaultMaxSkills
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = DefaultMaxEntries
	}
	if l.MaxResolved <= 0 {
		l.MaxResolved = DefaultMaxResolved
	}
	if l.MaxLoadBytes <= 0 {
		l.MaxLoadBytes = DefaultMaxLoadBytes
	}
	return l
}

type Authority struct {
	Plugin     string `json:"plugin"`
	Generation uint64 `json:"generation"`
	Token      string `json:"token"`
}

func (a Authority) validate() error {
	if !namePattern.MatchString(a.Plugin) {
		return errors.New("plugin authority name is invalid")
	}
	if a.Generation == 0 {
		return errors.New("plugin authority generation must be positive")
	}
	if strings.TrimSpace(a.Token) == "" || strings.ContainsAny(a.Token, "\x00\r\n") {
		return errors.New("plugin authority token is invalid")
	}
	return nil
}

type AuthorityVerifier interface {
	VerifySkillAuthority(context.Context, Authority) error
}

type AuthorityVerifierFunc func(context.Context, Authority) error

func (f AuthorityVerifierFunc) VerifySkillAuthority(ctx context.Context, authority Authority) error {
	if f == nil {
		return errors.New("plugin skill authority verifier is required")
	}
	return f(ctx, authority)
}

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	localePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:[-_][A-Za-z0-9]{2,8})*$`)
)

func localizedValue(values map[string]string, locale string) string {
	locale = normalizeLocale(locale)
	if locale == "" {
		return ""
	}
	if value := values[locale]; value != "" {
		return value
	}
	if language, _, found := strings.Cut(locale, "-"); found {
		return values[language]
	}
	return ""
}

func normalizeLocale(locale string) string {
	locale = strings.TrimSpace(strings.ReplaceAll(locale, "_", "-"))
	if locale == "" || !localePattern.MatchString(locale) {
		return ""
	}
	parts := strings.Split(locale, "-")
	parts[0] = strings.ToLower(parts[0])
	for index := 1; index < len(parts); index++ {
		parts[index] = strings.ToUpper(parts[index])
	}
	return strings.Join(parts, "-")
}
