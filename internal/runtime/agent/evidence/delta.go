package evidence

import "sort"

type ReadState struct {
	Path       string `json:"path"`
	Digest     string `json:"digest"`
	Turn       uint64 `json:"turn"`
	RepeatTurn uint64 `json:"repeat_turn,omitempty"`
	Repeats    int    `json:"repeats,omitempty"`
}

type HandleState struct {
	ID       string `json:"id"`
	Tool     string `json:"tool"`
	Turn     uint64 `json:"turn"`
	Consumed bool   `json:"consumed,omitempty"`
}

type Delta struct {
	Turn    uint64        `json:"turn"`
	Facts   []Fact        `json:"facts,omitempty"`
	Changes []Change      `json:"changes,omitempty"`
	Reads   []ReadState   `json:"reads,omitempty"`
	Handles []HandleState `json:"handles,omitempty"`
}

func (s *Set) Delta() Delta {
	if s == nil {
		return Delta{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Delta{Turn: s.turn}
	for _, fact := range s.facts {
		result.Facts = append(result.Facts, fact)
	}
	for path, entry := range s.changes {
		result.Changes = append(result.Changes, Change{
			Path: path, Turn: entry.turn, Read: entry.read,
			Verified: entry.verified, Diagnostics: entry.diagnostics,
		})
	}
	for path, entry := range s.reads {
		result.Reads = append(result.Reads, ReadState{
			Path: path, Digest: entry.digest, Turn: entry.turn,
			RepeatTurn: entry.repeatTurn, Repeats: entry.repeats,
		})
	}
	for id, entry := range s.handles {
		result.Handles = append(result.Handles, HandleState{
			ID: id, Tool: entry.tool, Turn: entry.turn,
			Consumed: entry.consumed,
		})
	}
	sort.Slice(result.Facts, func(i, j int) bool {
		left, right := result.Facts[i], result.Facts[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Line < right.Line
	})
	sort.Slice(result.Changes, func(i, j int) bool {
		return result.Changes[i].Path < result.Changes[j].Path
	})
	sort.Slice(result.Reads, func(i, j int) bool {
		return result.Reads[i].Path < result.Reads[j].Path
	})
	sort.Slice(result.Handles, func(i, j int) bool {
		return result.Handles[i].ID < result.Handles[j].ID
	})
	return result
}

func ApplyDelta(delta Delta) *Set {
	result := New()
	result.turn = delta.Turn
	for _, fact := range delta.Facts {
		result.facts[factKey{
			kind: fact.Kind, path: fact.Path, line: fact.Line,
		}] = fact
	}
	for _, value := range delta.Changes {
		result.changes[value.Path] = &change{
			turn: value.Turn, read: value.Read,
			verified: value.Verified, diagnostics: value.Diagnostics,
		}
	}
	for _, value := range delta.Reads {
		result.reads[value.Path] = &read{
			digest: value.Digest, turn: value.Turn,
			repeatTurn: value.RepeatTurn, repeats: value.Repeats,
		}
	}
	for _, value := range delta.Handles {
		result.handles[value.ID] = &handle{
			tool: value.Tool, turn: value.Turn, consumed: value.Consumed,
		}
	}
	return result
}
