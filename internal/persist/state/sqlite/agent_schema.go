package sqlite

const agentTopologySchema = `
CREATE TABLE agent_nodes (
    workspace_root TEXT NOT NULL,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    path TEXT NOT NULL,
    execution_root TEXT NOT NULL DEFAULT '',
    parent_agent_id TEXT NOT NULL DEFAULT '',
    parent_path TEXT NOT NULL DEFAULT '/root',
    thread_id TEXT NOT NULL,
    turn_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    role TEXT NOT NULL,
    profile TEXT NOT NULL DEFAULT '',
    stance TEXT NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    worktree TEXT NOT NULL DEFAULT '',
    isolated INTEGER NOT NULL DEFAULT 0,
    serialized INTEGER NOT NULL DEFAULT 0,
    base_revision TEXT NOT NULL DEFAULT '',
    task_name TEXT NOT NULL DEFAULT '',
    owned_paths_json BLOB NOT NULL DEFAULT '[]'
        CHECK (json_valid(owned_paths_json)),
    last_message TEXT NOT NULL DEFAULT '',
    max_steps INTEGER NOT NULL DEFAULT 0,
    max_tokens INTEGER NOT NULL DEFAULT 0,
    max_cost_microunits INTEGER NOT NULL DEFAULT 0,
    reserved_tokens INTEGER NOT NULL DEFAULT 0,
    reserved_microunits INTEGER NOT NULL DEFAULT 0,
    operation_id TEXT NOT NULL,
    actor TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    event_id TEXT NOT NULL,
    source_sequence INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (workspace_root, agent_id),
    UNIQUE (workspace_root, path)
);
CREATE INDEX agent_nodes_workspace_parent_status
ON agent_nodes(workspace_root, session_id, parent_agent_id, status);

CREATE TABLE agent_messages (
    workspace_root TEXT NOT NULL,
    session_id TEXT NOT NULL,
    id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    from_agent_id TEXT NOT NULL,
    to_agent_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    payload_ref TEXT NOT NULL DEFAULT '',
    body BLOB NOT NULL,
    trigger_turn INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    published_at TEXT,
    delivered_at TEXT,
    source_sequence INTEGER NOT NULL,
    PRIMARY KEY (workspace_root, session_id, id),
    UNIQUE (workspace_root, session_id, to_agent_id, sequence)
);
CREATE INDEX agent_messages_workspace_to_delivery
ON agent_messages(workspace_root, session_id, to_agent_id, delivered_at, sequence);

CREATE TABLE agent_results (
    workspace_root TEXT NOT NULL,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    turn_id TEXT NOT NULL DEFAULT '',
    result_json BLOB NOT NULL,
    receipt_ref TEXT NOT NULL DEFAULT '',
    source_sequence INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (workspace_root, session_id, agent_id),
    FOREIGN KEY (workspace_root, agent_id)
        REFERENCES agent_nodes(workspace_root, agent_id) ON DELETE CASCADE
);

CREATE TABLE agent_budget_ledger (
    workspace_root TEXT NOT NULL,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    reserved_tokens INTEGER NOT NULL DEFAULT 0,
    spent_tokens INTEGER NOT NULL DEFAULT 0,
    reserved_microunits INTEGER NOT NULL DEFAULT 0,
    spent_microunits INTEGER NOT NULL DEFAULT 0,
    reserved_slots INTEGER NOT NULL DEFAULT 0,
    released INTEGER NOT NULL DEFAULT 0,
    source_sequence INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (workspace_root, session_id, agent_id),
    FOREIGN KEY (workspace_root, agent_id)
        REFERENCES agent_nodes(workspace_root, agent_id) ON DELETE CASCADE
);

CREATE TABLE agent_integrations (
    workspace_root TEXT NOT NULL,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    preview_digest TEXT NOT NULL,
    status TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    candidate_json BLOB NOT NULL,
    source_sequence INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (workspace_root, session_id, agent_id, preview_digest),
    FOREIGN KEY (workspace_root, agent_id)
        REFERENCES agent_nodes(workspace_root, agent_id) ON DELETE CASCADE
);
CREATE INDEX agent_integrations_workspace_status
ON agent_integrations(workspace_root, session_id, status, updated_at);
`
