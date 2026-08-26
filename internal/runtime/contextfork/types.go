package contextfork

type ContextSourceRef struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
}

type ContextBlock struct {
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type ContextMessage struct {
	Role   string         `json:"role"`
	Turn   uint64         `json:"turn"`
	Blocks []ContextBlock `json:"blocks"`
}

type ContextRelevantFile struct {
	Path     string   `json:"path"`
	Sources  []string `json:"sources,omitempty"`
	Critical bool     `json:"critical,omitempty"`
}

type ContextEvidence struct {
	Summary string `json:"summary"`
	Handle  string `json:"handle"`
}

type ParentContextSnapshot struct {
	SourceThread    string                `json:"source_thread"`
	SourceTurn      string                `json:"source_turn"`
	AvailableTokens uint64                `json:"available_tokens,omitempty"`
	ParentGoal      string                `json:"parent_goal,omitempty"`
	UserRequest     string                `json:"user_request,omitempty"`
	Messages        []ContextMessage      `json:"messages,omitempty"`
	RelevantFiles   []ContextRelevantFile `json:"relevant_files,omitempty"`
	Evidence        []ContextEvidence     `json:"evidence,omitempty"`
	WorkspaceRules  []string              `json:"workspace_rules,omitempty"`
}
