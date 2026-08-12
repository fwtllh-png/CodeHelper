package permissions

import (
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// Store owns the mutable workspace permission document. Wire receives this as
// a construction capability; Guard uses it for durable allow decisions.
type Store struct {
	workspace string
	bundle    Bundle
	mu        sync.Mutex
}

func OpenStore(workspace string) (*Store, error) {
	bundle, err := Load(workspace)
	if err != nil {
		return nil, err
	}
	return &Store{workspace: workspace, bundle: bundle}, nil
}

func (s *Store) Rules() []policy.Rule {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]policy.Rule(nil), s.bundle.Rules...)
}

func (s *Store) AppendAllow(
	invocation policy.Invocation,
) (policy.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, err := RuleFromInvocation(invocation)
	if err != nil {
		return policy.Rule{}, err
	}
	bundle, err := AppendAllow(s.workspace, rule)
	if err != nil {
		return policy.Rule{}, err
	}
	s.bundle = bundle
	return rule, nil
}
