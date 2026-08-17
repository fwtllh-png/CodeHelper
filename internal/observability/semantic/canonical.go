package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
)

func CanonicalJSON(graph Graph) ([]byte, error) {
	cloned, err := cloneGraph(graph)
	if err != nil {
		return nil, err
	}
	normalize(&cloned)
	return json.Marshal(cloned)
}

func Digest(graph Graph) (string, error) {
	encoded, err := CanonicalJSON(graph)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func cloneGraph(graph Graph) (Graph, error) {
	encoded, err := json.Marshal(graph)
	if err != nil {
		return Graph{}, err
	}
	var cloned Graph
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return Graph{}, err
	}
	return cloned, nil
}

func normalize(graph *Graph) {
	if graph == nil {
		return
	}
	for key, node := range graph.Runtimes {
		sortEvidence(node.Evidence)
		graph.Runtimes[key] = node
	}
	for key, node := range graph.Threads {
		slices.Sort(node.TurnIDs)
		sortEvidence(node.Evidence)
		graph.Threads[key] = node
	}
	for key, node := range graph.Turns {
		sortEvidence(node.Evidence)
		graph.Turns[key] = node
	}
	for key, node := range graph.InferenceCalls {
		sortEvidence(node.Evidence)
		graph.InferenceCalls[key] = node
	}
	for key, node := range graph.ToolAttempts {
		sortEvidence(node.Evidence)
		sort.Slice(node.ModelVisible, func(left, right int) bool {
			return node.ModelVisible[left].Reference.Digest <
				node.ModelVisible[right].Reference.Digest
		})
		graph.ToolAttempts[key] = node
	}
	for key, node := range graph.Effects {
		slices.Sort(node.Requeues)
		sortEvidence(node.Evidence)
		graph.Effects[key] = node
	}
	for key, node := range graph.Approvals {
		sortEvidence(node.Evidence)
		graph.Approvals[key] = node
	}
	for key, node := range graph.TerminalOps {
		sortEvidence(node.Evidence)
		graph.TerminalOps[key] = node
	}
	for key, node := range graph.Verifications {
		sortEvidence(node.Evidence)
		graph.Verifications[key] = node
	}
	for key, node := range graph.Agents {
		sortEvidence(node.Evidence)
		graph.Agents[key] = node
	}
	sort.Slice(graph.Interactions, func(left, right int) bool {
		if graph.Interactions[left].Sequence != graph.Interactions[right].Sequence {
			return graph.Interactions[left].Sequence <
				graph.Interactions[right].Sequence
		}
		return graph.Interactions[left].ID < graph.Interactions[right].ID
	})
	sort.Slice(graph.Visibility, func(left, right int) bool {
		if graph.Visibility[left].Sequence != graph.Visibility[right].Sequence {
			return graph.Visibility[left].Sequence <
				graph.Visibility[right].Sequence
		}
		return graph.Visibility[left].ID < graph.Visibility[right].ID
	})
	sort.Slice(graph.Inconsistencies, func(left, right int) bool {
		leftSequence := firstSequence(graph.Inconsistencies[left].Sequences)
		rightSequence := firstSequence(graph.Inconsistencies[right].Sequences)
		if leftSequence != rightSequence {
			return leftSequence < rightSequence
		}
		if graph.Inconsistencies[left].Code != graph.Inconsistencies[right].Code {
			return graph.Inconsistencies[left].Code <
				graph.Inconsistencies[right].Code
		}
		return graph.Inconsistencies[left].ObjectID <
			graph.Inconsistencies[right].ObjectID
	})
	sort.Slice(graph.Unknowns, func(left, right int) bool {
		if graph.Unknowns[left].Sequence != graph.Unknowns[right].Sequence {
			return graph.Unknowns[left].Sequence < graph.Unknowns[right].Sequence
		}
		if graph.Unknowns[left].Code != graph.Unknowns[right].Code {
			return graph.Unknowns[left].Code < graph.Unknowns[right].Code
		}
		return graph.Unknowns[left].ObjectID < graph.Unknowns[right].ObjectID
	})
}

func sortEvidence(values []Evidence) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].Sequence != values[right].Sequence {
			return values[left].Sequence < values[right].Sequence
		}
		return values[left].ObservationID < values[right].ObservationID
	})
}

func firstSequence(values []uint64) uint64 {
	if len(values) == 0 {
		return 0
	}
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func jsonUnmarshal(content []byte, target any) error {
	return json.Unmarshal(content, target)
}
