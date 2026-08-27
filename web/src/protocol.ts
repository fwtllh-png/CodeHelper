export type Cursor = number;

export interface Problem {
  version: number;
  code: string;
  message: string;
  retryable: boolean;
  details?: Record<string, unknown>;
}

export interface Envelope<T> {
  version: number;
  request_id?: string;
  result?: T;
  problem?: Problem;
}

export interface WorkspaceIdentity {
  version: number;
  root_id: string;
  editor_uri: string;
  runtime_path: string;
  remote_name?: string;
}

export interface Bootstrap {
  protocol_version: number;
  server_build: string;
  token: string;
  ready: boolean;
  draining: boolean;
  workspace_root?: string;
  workspace?: WorkspaceIdentity;
  can_open_path?: boolean;
  setup_required?: boolean;
  setup_catalog?: SetupCatalog;
  workspace_catalog?: WorkspaceCatalog;
  problem?: Problem;
}

export interface SetupProvider {
  id: string;
  display_name: string;
  protocol: string;
  requires_api_key: boolean;
  custom?: boolean;
}

export interface SetupCatalog {
  version: number;
  providers: SetupProvider[];
}

export interface SetupRequest {
  provider: string;
  model: string;
  api_key?: string;
  base_url?: string;
  protocol?: string;
}

export interface SetupResult {
  ready: boolean;
}

export interface WorkspaceDescriptor {
  id: string;
  root: string;
  label: string;
  ready: boolean;
  removable: boolean;
  session_count: number;
  problem?: string;
  git?: WorkspaceGitState;
}

export interface WorkspaceGitState {
  repository: boolean;
  branch?: string;
  branches?: string[];
  detached?: boolean;
  dirty?: boolean;
}

export interface WorkspaceCatalog {
  version: number;
  default_workspace_id: string;
  workspaces: WorkspaceDescriptor[];
}

export interface WorkspaceAddResult {
  workspace: WorkspaceDescriptor;
}

export interface WorkspaceDirectoryResult {
  path?: string;
  cancelled?: boolean;
}

export interface SessionSummary {
  version: number;
  revision: number;
  session_id: string;
  thread_id: string;
  title: string;
  status: string;
  pinned: boolean;
  archived: boolean;
  isolation: "shared" | "worktree";
  workspace_root: string;
  workspace_label: string;
  provider?: string;
  model?: string;
  mode?: string;
  latest_turn_id?: string;
  latest_sequence: Cursor;
  pending_approvals: number;
  pending_inputs: number;
  checkpoint_count: number;
  changed_files: number;
  total_tokens: number;
  cost_microunits: number;
  cost_known: boolean;
  created_at: string;
  updated_at: string;
}

export interface SessionList {
  version: number;
  query?: string;
  sessions: SessionSummary[];
}

export interface SessionBinding {
  session_id: string;
  thread_id: string;
  workspace_root: string;
  provider: string;
  model: string;
  isolation: string;
}

export interface SessionLifecycleUpdate {
  session: SessionSummary;
}

export interface SessionDeleteResult {
  version: number;
  session_id: string;
  thread_id: string;
  deleted_at: string;
}

export interface RuntimeEvent {
  version: number;
  id: string;
  kind: string;
  operation_id: string;
  thread_id: string;
  turn_id: string;
  item_id: string;
  sequence: Cursor;
  created_at: string;
  data: Record<string, unknown>;
}

export interface PresentationSnapshot {
  version: number;
  session_id: string;
  thread_id: string;
  session_revision: number;
  through_sequence: Cursor;
  events: RuntimeEvent[];
  history_truncated_before?: Cursor;
}

export interface QueuedTurn {
  queue_id: string;
  thread_id: string;
  source_turn_id: string;
  prompt: string;
  display_prompt?: string;
  intent?: string;
  workspace_identity?: WorkspaceIdentity;
  context?: EditorContextReference[];
  added_sequence: Cursor;
  created_at: string;
  updated_at: string;
}

export interface TurnQueue {
  version: number;
  items: QueuedTurn[];
}

export interface SessionHistoryPage {
  session_id: string;
  events: RuntimeEvent[];
  next_sequence: Cursor;
  more: boolean;
  previous_sequence?: Cursor;
  more_before?: boolean;
}

export interface EventFrame {
  type: "hello" | "event" | "watermark" | "resync" | "desync";
  protocol_version: number;
  session_id?: string;
  sequence: Cursor;
  event?: RuntimeEvent;
  problem?: Problem;
}

export interface OperationReceipt {
  operation_id: string;
  kind: string;
  thread_id: string;
  turn_id: string;
  item_id: string;
  accepted: boolean;
}

export interface SessionProfile {
  version: number;
  revision: number;
  mode: "plan" | "act" | "operate";
  planning_policy?: "off" | "adaptive" | "required";
  plan_approval?: "manual" | "auto";
  provider: string;
  model: string;
  reasoning_effort?: string;
  enabled_tool_ids?: string[];
  approval_posture: string;
  execution_target: string;
  max_steps: number;
  prompt_cache_revision?: number;
}

export interface SessionProfileSnapshot {
  profile: SessionProfile;
  capabilities: {
    provider: string;
    model: string;
    mutable_fields: string[];
    model_capabilities: ModelCapabilities;
  };
}

export interface SessionProfileUpdateResult {
  profile: SessionProfile;
  prompt_cache_reset: boolean;
  reset_reason?: string;
}

export interface AgentPresetProfile {
  mode: SessionProfile["mode"];
  planning_policy?: SessionProfile["planning_policy"];
  plan_approval?: SessionProfile["plan_approval"];
  provider: string;
  model: string;
  reasoning_effort?: string;
  enabled_tool_ids?: string[];
  approval_posture: string;
  execution_target: string;
  max_steps: number;
}

export interface AgentPreset {
  version: number;
  id: string;
  revision: number;
  name: string;
  description?: string;
  scope: "workspace";
  profile: AgentPresetProfile;
  created_at: string;
  updated_at: string;
}

export interface AgentPresetList {
  version: number;
  revision: number;
  presets: AgentPreset[];
}

export interface AgentPresetMutationResult {
  version: number;
  revision: number;
  preset?: AgentPreset;
  deleted_id?: string;
  duplicate?: boolean;
}

export interface AgentPresetApplyResult {
  version: number;
  preset_id: string;
  profile_update: SessionProfileUpdateResult;
  restart_required: boolean;
  restart_reason?: string;
}

export interface ProviderCatalogEntry {
  id: string;
  display_name: string;
  selected: boolean;
  availability: "available" | "unavailable";
  reason?: string;
}

export interface ProviderCatalog {
  version: number;
  providers: ProviderCatalogEntry[];
}

export interface ModelCapabilities {
  display_name: string;
  context_window: number;
  max_output_tokens: number;
  streaming: boolean;
  reasoning: boolean;
  tool_calls: boolean;
  parallel_tool_calls: "supported" | "unsupported" | "unknown";
  native_search: boolean;
  vision: boolean;
  image_input: boolean;
  prompt_cache: boolean;
  reasoning_efforts?: string[];
  default_reasoning_effort?: string;
  credential_status: "configured" | "missing" | "invalid" | "unknown";
  availability: "available" | "unavailable";
  unavailable_reason?: string;
  selection_mode: "hot" | "restart_required" | "fixed";
}

export interface ModelCatalogEntry {
  provider: string;
  id: string;
  source?: "catalog" | "connection_baseline";
  selected: boolean;
  capabilities: ModelCapabilities;
}

export interface ModelCatalog {
  version: number;
  models: ModelCatalogEntry[];
}

export interface ModelTestResult {
  provider: string;
  model: string;
  status: "available" | "not_listed";
  detail: string;
  tested_at: string;
}

export interface WorkspaceConnection {
  provider: string;
  endpoint: string;
  protocol: string;
}

export interface ToolCatalogEntry {
  id: string;
  name: string;
  description: string;
  source_kind: string;
  source_label: string;
  capability: string;
  access_mode: string;
  risk_level: string;
  sandbox_requirement: string;
  policy_state: string;
  policy_reason: string;
  constitution_state: string;
  constitution_reason: string;
  availability: string;
  unavailable_reason?: string;
  state: string;
  revision: number;
  enabled: boolean;
  guarded: boolean;
}

export interface ToolCatalog {
  version: number;
  catalog_id: string;
  generation: number;
  digest: string;
  tools: ToolCatalogEntry[];
}

export interface SessionCheckpoint {
  version: number;
  id: string;
  session_id: string;
  thread_id: string;
  turn_id: string;
  cursor: Cursor;
  status: "completed" | "interrupted";
  summary: string;
  profile_revision: number;
  changed_files: number;
  external_side_effects: boolean;
  can_restore: boolean;
  can_fork: boolean;
  created_at: string;
}

export interface CheckpointList {
  version: number;
  session_id: string;
  checkpoints: SessionCheckpoint[];
}

export interface CheckpointForkResult {
  version: number;
  checkpoint: SessionCheckpoint;
  session_id: string;
  thread_id: string;
  parent_thread_id: string;
  exact_context: boolean;
  workspace_claims_valid: boolean;
}

export interface SessionPlanArtifact {
  version: number;
  id: string;
  session_id: string;
  thread_id: string;
  turn_id: string;
  cursor: Cursor;
  status: "ready";
  body: string;
  document?: PlanDocument;
  profile_revision: number;
  can_implement: boolean;
  can_autopilot: boolean;
  created_at: string;
}

export interface PlanStep {
  id: string;
  title: string;
  status: "pending" | "in_progress" | "done";
  dependencies?: string[];
  expected_evidence?: string;
  affected_files?: string[];
}

export interface PlanDocument {
  version: 1;
  revision?: number;
  supersedes_id?: string;
  title?: string;
  objective?: string;
  context_summary?: string;
  steps: PlanStep[];
  sources_used?: string[];
  critical_files?: string[];
  constraints?: string[];
  recommended_approach?: string;
  verification_plan?: string;
  risks_and_unknowns?: string;
  handoff_packet?: string;
  file_baseline?: Array<{
    path: string;
    digest?: string;
    missing?: boolean;
  }>;
}

export interface SessionPlanSnapshot {
  version: number;
  artifact?: SessionPlanArtifact;
}

export interface TaskSummary {
  id: string;
  kind: string;
  state: string;
  reason?: string;
  failure_reason?: string;
}

export interface AgentSummary {
  id: string;
  role: string;
  status: string;
  last_message?: string;
}

export interface UsageRollup {
  turns: number;
  calls: number;
  total_tokens: number;
  cost_microunits: number;
  cost_known: boolean;
}

export interface TaskList {
  tasks: TaskSummary[];
}

export interface AgentList {
  agents: AgentSummary[];
}

export interface UsageQueryResult {
  usage: unknown[];
  rollup: UsageRollup;
}

export type TraceSpanKind =
  | "turn"
  | "model"
  | "tool"
  | "approval"
  | "verification";

export interface TraceSpan {
  id: number;
  parent_id?: number;
  kind: TraceSpanKind;
  status: "open" | "ok" | "error" | "canceled";
  started_at: string;
  ended_at?: string;
  duration_ms?: number;
  sample?: number;
  call_id?: string;
}

export interface TraceTurn {
  turn_id: string;
  started_at?: string;
  ended_at?: string;
  status: "unavailable" | "open" | "ok" | "error" | "canceled";
  spans: TraceSpan[];
}

export interface TraceSnapshot {
  version: number;
  session_id: string;
  through_sequence: Cursor;
  turns: TraceTurn[];
}

export interface ExtensionProjection {
  kind: "plugin" | "skill";
  name: string;
  version?: string;
  source?: string;
  publisher?: string;
  trust?: string;
  digest?: string;
  generation?: number;
  enabled: boolean;
  health: string;
  permissions?: string[];
  capabilities?: Array<{
    id: string;
    kind: string;
    enabled: boolean;
    source_digest?: string;
    permission_digest?: string;
    authority_token?: string;
  }>;
  last_action?: string;
  changed_at?: string;
}

export type ExtensionControlAction =
  | "detail"
  | "health"
  | "permissions"
  | "receipts"
  | "trust"
  | "enable"
  | "disable"
  | "revoke"
  | "security_revoke"
  | "install"
  | "update"
  | "rollback"
  | "capability_enable"
  | "capability_disable"
  | "lint"
  | "lock"
  | "verify";

export interface ExtensionControlResult {
  operation_id?: string;
  duplicate?: boolean;
  revision: number;
  extensions?: ExtensionProjection[];
  detail?: unknown;
  receipt?: {
    operation_id: string;
    action: string;
    kind: string;
    name?: string;
    status: string;
    digest: string;
    revision: number;
    occurred_at: string;
  };
  receipts?: Array<{
    operation_id: string;
    action: string;
    kind: string;
    name?: string;
    status: string;
    digest: string;
    revision: number;
    occurred_at: string;
  }>;
  diagnostics?: unknown;
}

export interface EditPlanFile {
  path: string;
  kind: string;
}

export interface EditPlan {
  id: string;
  diff: string;
  files: EditPlanFile[];
}

export interface SessionMergeResult {
  session_id: string;
  thread_id: string;
  action: "preview" | "apply";
  plan: EditPlan;
}

export interface WorkspaceEntry {
  path: string;
  kind: "file" | "directory";
  size?: number;
}

export interface WorkspaceBrowseResult {
  path: string;
  entries: WorkspaceEntry[];
  more: boolean;
}

export interface WorkspaceSearchMatch {
  path: string;
  line: number;
  column: number;
  preview: string;
}

export interface WorkspaceSearchResult {
  query: string;
  matches: WorkspaceSearchMatch[];
  more: boolean;
}

export interface WorkspaceOpenResult {
  opened: true;
  path: string;
}

export interface WorkspaceResource {
  path: string;
  uri: string;
  document_version: number;
  content: string;
  digest: string;
  bytes: number;
  content_handle: string;
}

export interface WorkspaceImage {
  path: string;
  uri: string;
  document_version: number;
  digest: string;
  bytes: number;
  media_type: "image/png" | "image/jpeg" | "image/gif" | "image/webp";
  label: string;
  content_handle: string;
}

export interface EditorPosition {
  line: number;
  character: number;
}

export interface EditorRange {
  start: EditorPosition;
  end: EditorPosition;
}

export interface EditorSymbol {
  name: string;
  kind: string;
  selection_range: EditorRange;
}

export interface EditorDiagnostic {
  range: EditorRange;
  severity: "error" | "warning" | "information" | "hint";
  code?: string;
  message: string;
  source?: string;
}

export interface EditorContextReference {
  kind: "file" | "selection" | "symbol" | "diagnostics" | "image" | "attachment" | "terminal" | "git_diff";
  source: "composer" | "selection_command" | "code_action" | "native_picker";
  uri?: string;
  path?: string;
  document_version?: number;
  digest: string;
  range?: EditorRange;
  symbol?: EditorSymbol;
  diagnostics?: EditorDiagnostic[];
  omitted_diagnostics?: number;
  label?: string;
  media_type?: string;
  content?: string;
  explicit: true;
}

export interface WorkspaceSymbol {
  path: string;
  name: string;
  kind: string;
  container?: string;
  line: number;
  uri: string;
  document_version: number;
  digest: string;
  range: EditorRange;
  selection_range: EditorRange;
}

export interface WorkspaceSymbolList {
  query: string;
  status: string;
  detail?: string;
  symbols: WorkspaceSymbol[];
}

export interface WorkspaceDiagnosticContext {
  call_id: string;
  tool: string;
  status: string;
  message?: string;
  context: EditorContextReference;
}

export interface WorkspaceDiagnostics {
  session_id: string;
  thread_id: string;
  diagnostics: WorkspaceDiagnosticContext[];
}

export interface WorkspaceDiff {
  session_id: string;
  thread_id: string;
  diff: string;
  digest: string;
}

export interface CredentialStatus {
  reference: {
    kind: string;
    name: string;
  };
  configured: boolean;
  validation: "not_validated" | "valid" | "invalid";
  validation_detail?: string;
  validated_at?: string;
  restart_required?: boolean;
}

export interface SessionExport {
  version: number;
  exported_at: string;
  session: SessionSummary;
  snapshot: PresentationSnapshot;
  integrity: {
    algorithm: "sha256";
    digest: string;
  };
}
