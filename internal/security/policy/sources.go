package policy

import (
	"errors"
	"fmt"
)

type AuthoritySource string

const (
	SourceManaged    AuthoritySource = "managed"
	SourceUser       AuthoritySource = "user"
	SourceRepository AuthoritySource = "repository"
)

func (r *Runtime) ReloadSources(user, repository []Rule) (uint64, error) {
	if r == nil {
		return 0, errors.New("policy runtime is required")
	}
	if err := ValidateRules(SourceUser, user); err != nil {
		return 0, err
	}
	if err := ValidateRules(SourceRepository, repository); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Revision == 0 {
		r.Revision = 1
	}
	r.Revision++
	r.User = append([]Rule(nil), user...)
	r.Repository = append([]Rule(nil), repository...)
	return r.Revision, nil
}

func (r *Runtime) AppendUserRule(rule Rule) (uint64, error) {
	if r == nil {
		return 0, errors.New("policy runtime is required")
	}
	if err := ValidateRules(SourceUser, []Rule{rule}); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Revision == 0 {
		r.Revision = 1
	}
	r.Revision++
	r.User = append(append([]Rule(nil), r.User...), rule)
	return r.Revision, nil
}

func ValidateRules(source AuthoritySource, rules []Rule) error {
	switch source {
	case SourceManaged, SourceUser, SourceRepository:
	default:
		return errors.New("unknown policy authority source")
	}
	for index, rule := range rules {
		if rule.Tool == "" {
			return fmt.Errorf("%s rule %d: tool is required", source, index)
		}
		switch rule.Action {
		case ActionAllow, ActionAsk, ActionDeny, ActionHold:
		default:
			return fmt.Errorf("%s rule %d: action is invalid", source, index)
		}
		if source == SourceRepository && rule.Action == ActionAllow {
			return fmt.Errorf("repository rule %d: repository authority cannot allow", index)
		}
		if rule.Action == ActionHold && rule.Code == "" {
			return fmt.Errorf("%s rule %d: hold code is required", source, index)
		}
		if rule.CommandPrefix != "" {
			if _, err := parseStaticPrefix(rule.CommandPrefix); err != nil {
				return fmt.Errorf("%s rule %d: %w", source, index, err)
			}
			if rule.Action == ActionAllow && unsafePersistentPrefix(rule.CommandPrefix) {
				return fmt.Errorf(
					"%s rule %d: unsafe broad command prefix cannot persist",
					source, index,
				)
			}
		}
	}
	return nil
}
