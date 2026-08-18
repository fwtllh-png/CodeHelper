package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const workflowNodeExecutor = "workflow_node"

type CompileOptions struct {
	RunID        protocol.RunID
	SessionID    string
	Workspace    string
	RootThreadID protocol.ThreadID
}

type Compiled struct {
	Submit  kernel.SubmitData
	Ordered []Node
}

func Compile(spec Spec, options CompileOptions) (Compiled, error) {
	if err := spec.Validate(); err != nil {
		return Compiled{}, err
	}
	ordered, err := spec.order()
	if err != nil {
		return Compiled{}, err
	}
	if options.RunID == "" {
		return Compiled{}, fmt.Errorf("%w: run id is required", ErrInvalidSpec)
	}
	if options.SessionID == "" {
		options.SessionID = "workflow_local"
	}
	if options.RootThreadID == "" {
		options.RootThreadID = protocol.ThreadID(
			"thread_workflow_" + string(options.RunID),
		)
	}
	nodes := make([]model.NodeSpec, 0, len(spec.Nodes))
	runAuthority := sha256.New()
	for _, node := range spec.Nodes {
		encoded, err := json.Marshal(node)
		if err != nil {
			return Compiled{}, fmt.Errorf("encode workflow node %q: %w", node.ID, err)
		}
		dependencies := node.dependencies()
		nodeSpec := model.NodeSpec{
			ID:              protocol.NodeID(node.ID),
			Kind:            workflowNodeKind(node.Kind),
			Dependencies:    make([]protocol.NodeID, len(dependencies)),
			AuthorityDigest: workflowAuthorityDigest(spec, node),
			Execution: &model.ExecutionSpec{
				TaskKind: string(node.Kind), Executor: workflowNodeExecutor,
				Payload: encoded, MaxAttempts: node.attempts(),
			},
		}
		_, _ = runAuthority.Write([]byte(nodeSpec.AuthorityDigest))
		for index, dependency := range dependencies {
			nodeSpec.Dependencies[index] = protocol.NodeID(dependency)
		}
		if node.When != nil {
			state, err := workflowProtocolState(node.When.Status)
			if err != nil {
				return Compiled{}, err
			}
			nodeSpec.Condition = &model.NodeCondition{
				NodeID: protocol.NodeID(node.When.Node), State: state,
			}
		}
		nodes = append(nodes, nodeSpec)
	}
	return Compiled{
		Submit: kernel.SubmitData{
			Kind: model.RunKindWorkflow, Source: "workflow",
			SessionID: options.SessionID, Workspace: options.Workspace,
			RootThreadID:     options.RootThreadID,
			DefinitionDigest: spec.Fingerprint(),
			AuthorityDigest:  hex.EncodeToString(runAuthority.Sum(nil)),
			Nodes:            nodes,
		},
		Ordered: ordered,
	}, nil
}

func workflowAuthorityDigest(spec Spec, node Node) string {
	encoded, _ := json.Marshal(struct {
		Role        string      `json:"role,omitempty"`
		Profile     string      `json:"profile,omitempty"`
		Permissions Permissions `json:"permissions"`
	}{
		Role: node.Role, Profile: node.Profile,
		Permissions: spec.EffectivePermissions(node),
	})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func workflowNodeKind(kind NodeKind) model.NodeKind {
	switch kind {
	case NodePhase:
		return model.NodeKindPhase
	case NodeParallel:
		return model.NodeKindJoin
	default:
		return model.NodeKindAgentTurn
	}
}

func workflowProtocolState(status NodeStatus) (protocol.NodeState, error) {
	switch status {
	case NodeStatusCompleted:
		return protocol.NodeStateSucceeded, nil
	case NodeStatusBlocked:
		return protocol.NodeStateBlocked, nil
	case NodeStatusFailed:
		return protocol.NodeStateFailed, nil
	case NodeStatusSkipped:
		return protocol.NodeStateSkipped, nil
	default:
		return "", fmt.Errorf(
			"%w: condition status %q is not terminal",
			ErrInvalidSpec,
			status,
		)
	}
}
