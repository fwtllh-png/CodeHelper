// Package parallel provides a shared orchestration governor for fan-out workers.
package parallel

import (
	"context"
	"errors"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
)

var (
	ErrConcurrency = errors.New("parallel concurrency budget exhausted")
	ErrAdmission   = errors.New("parallel admission rejected")
)

type Options struct {
	Governor *rlm.Governor
	Limit    int
}

type Runner struct {
	gov   *rlm.Governor
	limit int
}

func New(options Options) *Runner {
	limit := options.Limit
	if limit <= 0 {
		limit = 8
	}
	return &Runner{gov: options.Governor, limit: limit}
}

func (r *Runner) Map(ctx context.Context, depth int, items []string, fn func(context.Context, string) (string, error)) ([]string, error) {
	if r.gov == nil {
		return nil, errors.New("governor is required")
	}
	if len(items) == 0 {
		return nil, nil
	}
	lease, err := r.gov.Admit(depth, uint64(len(items)), 0)
	if err != nil {
		return nil, err
	}
	defer r.gov.Release(lease)

	results := make([]string, len(items))
	errCh := make(chan error, len(items))
	sem := make(chan struct{}, r.limit)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(i int, item string) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}
			nested, err := r.gov.Admit(depth+1, 1, 0)
			if err != nil {
				errCh <- err
				return
			}
			defer r.gov.Release(nested)
			out, err := fn(ctx, item)
			if err != nil {
				errCh <- err
				return
			}
			results[i] = out
		}(i, item)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}
