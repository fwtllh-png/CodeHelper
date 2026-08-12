package workingset

import "sort"

type Observation struct {
	Path   string `json:"path"`
	Source Source `json:"source"`
	Turn   uint64 `json:"turn"`
}

type Delta struct {
	Observations []Observation `json:"observations,omitempty"`
}

func (l *Ledger) Delta() Delta {
	if l == nil {
		return Delta{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var result Delta
	for path, record := range l.records {
		for source, turn := range record.sources {
			result.Observations = append(result.Observations, Observation{
				Path: path, Source: source, Turn: turn,
			})
		}
	}
	sort.Slice(result.Observations, func(i, j int) bool {
		left, right := result.Observations[i], result.Observations[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Source < right.Source
	})
	return result
}

func ApplyDelta(delta Delta) *Ledger {
	result := New()
	for _, observation := range delta.Observations {
		result.Observe(observation.Source, observation.Turn, observation.Path)
	}
	return result
}
