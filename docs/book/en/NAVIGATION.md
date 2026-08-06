# CodeHelper Agent Engineering Book: Navigation

[简体中文](../zh-CN/NAVIGATION.md) | English

This navigation is generated from `docs/book/catalog.json`. Planned chapters are visible without empty placeholder files.

## Status

- `planned`: catalog entry only; content has not started.
- `draft`: bilingual chapter files exist but are not fully verified.
- `verified`: bilingual content, source references, and commands passed the chapter gate.

## Stage 1 Reading Path

The first delivery milestone contains these six chapters:

- [Why Agents Need a Governed Runtime](./01-agent-engineering/05-why-governed-runtime.md) — `agent-why-governed-runtime` — `verified` — planned path: `01-agent-engineering/05-why-governed-runtime.md`
- [CodeHelper System Architecture](./02-codehelper-overview/02-system-architecture.md) — `overview-system-architecture` — `verified` — planned path: `02-codehelper-overview/02-system-architecture.md`
- [The Complete Lifecycle of an Agent Turn](./02-codehelper-overview/05-turn-lifecycle.md) — `overview-turn-lifecycle` — `verified` — planned path: `02-codehelper-overview/05-turn-lifecycle.md`
- [How Models, Context, and Tools Cooperate](./02-codehelper-overview/06-model-context-and-tool.md) — `overview-model-context-tool` — `verified` — planned path: `02-codehelper-overview/06-model-context-and-tool.md`
- [Guard, Approval, Constitution, and Sandbox](./07-security-governance/03-approval-constitution-sandbox.md) — `security-approval-sandbox` — `verified` — planned path: `07-security-governance/03-approval-constitution-sandbox.md`
- [Build and Trace the First Agent Turn](./13-hands-on-labs/01-first-agent-turn.md) — `lab-first-turn` — `verified` — planned path: `13-hands-on-labs/01-first-agent-turn.md`

## Complete Planned Contents

### Part 1: Entering Agent Engineering

- [From Chatbot to Agent](./01-agent-engineering/01-from-chatbot-to-agent.md) — `agent-from-chatbot-to-agent` — `verified` — planned path: `01-agent-engineering/01-from-chatbot-to-agent.md`
- [LLMs, Tokens, Context Windows, and Sampling](./01-agent-engineering/02-llm-token-and-context.md) — `agent-llm-token-context` — `verified` — planned path: `01-agent-engineering/02-llm-token-and-context.md`
- [ReAct, Planning, Tool Calling, and Reflection](./01-agent-engineering/03-react-planning-and-tools.md) — `agent-react-planning-tools` — `verified` — planned path: `01-agent-engineering/03-react-planning-and-tools.md`
- [Agents, Workflows, and Automation](./01-agent-engineering/04-agent-workflow-boundaries.md) — `agent-workflow-boundaries` — `verified` — planned path: `01-agent-engineering/04-agent-workflow-boundaries.md`
- [Why Agents Need a Governed Runtime](./01-agent-engineering/05-why-governed-runtime.md) — `agent-why-governed-runtime` — `verified` — planned path: `01-agent-engineering/05-why-governed-runtime.md`

### Part 2: Understanding CodeHelper

- [Positioning, Value, and Non-Goals](./02-codehelper-overview/01-positioning-and-non-goals.md) — `overview-positioning` — `verified` — planned path: `02-codehelper-overview/01-positioning-and-non-goals.md`
- [CodeHelper System Architecture](./02-codehelper-overview/02-system-architecture.md) — `overview-system-architecture` — `verified` — planned path: `02-codehelper-overview/02-system-architecture.md`
- [Package Ownership and Dependency Direction](./02-codehelper-overview/03-package-ownership.md) — `overview-package-ownership` — `verified` — planned path: `02-codehelper-overview/03-package-ownership.md`
- [Operation, Event, Receipt, and Projection](./02-codehelper-overview/04-runtime-vocabulary.md) — `overview-runtime-vocabulary` — `verified` — planned path: `02-codehelper-overview/04-runtime-vocabulary.md`
- [The Complete Lifecycle of an Agent Turn](./02-codehelper-overview/05-turn-lifecycle.md) — `overview-turn-lifecycle` — `verified` — planned path: `02-codehelper-overview/05-turn-lifecycle.md`
- [How Models, Context, and Tools Cooperate](./02-codehelper-overview/06-model-context-and-tool.md) — `overview-model-context-tool` — `verified` — planned path: `02-codehelper-overview/06-model-context-and-tool.md`

### Part 3: Runtime Kernel

- [Protocol and Stable Data Contracts](./03-runtime-kernel/01-protocol.md) — `runtime-protocol` — `verified` — planned path: `03-runtime-kernel/01-protocol.md`
- [Application Runtime and State Projection](./03-runtime-kernel/02-application-runtime.md) — `runtime-app` — `verified` — planned path: `03-runtime-kernel/02-application-runtime.md`
- [The Model and Tool Execution Loop](./03-runtime-kernel/03-agent-loop.md) — `runtime-agent-loop` — `verified` — planned path: `03-runtime-kernel/03-agent-loop.md`
- [Dependency Construction and Capability Wiring](./03-runtime-kernel/04-dependency-wiring.md) — `runtime-wiring` — `verified` — planned path: `03-runtime-kernel/04-dependency-wiring.md`
- [Streaming, Cancellation, and Error Taxonomy](./03-runtime-kernel/05-streaming-cancellation-errors.md) — `runtime-stream-cancel-errors` — `verified` — planned path: `03-runtime-kernel/05-streaming-cancellation-errors.md`
- [Resume, Recovery, and Idempotency](./03-runtime-kernel/06-resume-and-recovery.md) — `runtime-resume-recovery` — `verified` — planned path: `03-runtime-kernel/06-resume-and-recovery.md`

### Part 4: Models and Providers

- [Chat Completion and Responses Protocols](./04-model-and-provider/01-wire-protocols.md) — `model-wire-protocols` — `verified` — planned path: `04-model-and-provider/01-wire-protocols.md`
- [Provider Adapters, Model Catalog, and Wire IDs](./04-model-and-provider/02-provider-and-catalog.md) — `model-provider-catalog` — `verified` — planned path: `04-model-and-provider/02-provider-and-catalog.md`
- [Capability Negotiation and Route Resolution](./04-model-and-provider/03-capability-and-routing.md) — `model-capability-routing` — `verified` — planned path: `04-model-and-provider/03-capability-and-routing.md`
- [Streaming, Reasoning, Tool Calls, and Usage](./04-model-and-provider/04-streaming-reasoning-and-usage.md) — `model-stream-reasoning-usage` — `verified` — planned path: `04-model-and-provider/04-streaming-reasoning-and-usage.md`
- [Credential References and Secret Lifecycle](./04-model-and-provider/05-credential-lifecycle.md) — `model-credential-lifecycle` — `verified` — planned path: `04-model-and-provider/05-credential-lifecycle.md`
- [Retries, Rate Limits, Timeouts, and Failures](./04-model-and-provider/06-provider-failures.md) — `model-provider-failures` — `verified` — planned path: `04-model-and-provider/06-provider-failures.md`

### Part 5: Context Engineering

- [Prompts, Messages, and Context](./05-context-engineering/01-prompt-message-context.md) — `context-prompt-message` — `verified` — planned path: `05-context-engineering/01-prompt-message-context.md`
- [Workspace, Repository Index, and Editor Context](./05-context-engineering/02-workspace-index-editor.md) — `context-workspace-index-editor` — `verified` — planned path: `05-context-engineering/02-workspace-index-editor.md`
- [Context Sources, Priority, and Lifecycle](./05-context-engineering/03-source-priority-lifecycle.md) — `context-source-lifecycle` — `verified` — planned path: `05-context-engineering/03-source-priority-lifecycle.md`
- [Token Budgets, Compaction, and Information Loss](./05-context-engineering/04-budget-and-compaction.md) — `context-budget-compaction` — `verified` — planned path: `05-context-engineering/04-budget-and-compaction.md`
- [Memory, Snapshots, and Recovery](./05-context-engineering/05-memory-and-snapshot.md) — `context-memory-snapshot` — `verified` — planned path: `05-context-engineering/05-memory-and-snapshot.md`
- [Evaluating Context Quality](./05-context-engineering/06-context-quality.md) — `context-quality` — `verified` — planned path: `05-context-engineering/06-context-quality.md`

### Part 6: Tools and Governed Execution

- [Tool Schema, Registry, and Dynamic Catalog](./06-tools-and-execution/01-schema-registry-catalog.md) — `tool-schema-registry` — `verified` — planned path: `06-tools-and-execution/01-schema-registry-catalog.md`
- [File, Shell, and Agent Tools](./06-tools-and-execution/02-file-shell-agent-tools.md) — `tool-builtins` — `verified` — planned path: `06-tools-and-execution/02-file-shell-agent-tools.md`
- [The Tool Guard Pipeline](./06-tools-and-execution/03-tool-guard-pipeline.md) — `tool-guard-pipeline` — `verified` — planned path: `06-tools-and-execution/03-tool-guard-pipeline.md`
- [Edit Plans, Journals, and Receipts](./06-tools-and-execution/04-edit-journal-receipt.md) — `tool-edit-journal-receipt` — `verified` — planned path: `06-tools-and-execution/04-edit-journal-receipt.md`
- [Verification Gates and Evidence](./06-tools-and-execution/05-verification-and-evidence.md) — `tool-verification` — `verified` — planned path: `06-tools-and-execution/05-verification-and-evidence.md`
- [Feeding Tool Failures Back to the Model](./06-tools-and-execution/06-failure-feedback.md) — `tool-failure-feedback` — `verified` — planned path: `06-tools-and-execution/06-failure-feedback.md`

### Part 7: Security and Governance

- [Agent Runtime Threat Model](./07-security-governance/01-threat-model.md) — `security-threat-model` — `verified` — planned path: `07-security-governance/01-threat-model.md`
- [Mode, Posture, Policy, and Permission](./07-security-governance/02-mode-posture-policy.md) — `security-mode-policy` — `verified` — planned path: `07-security-governance/02-mode-posture-policy.md`
- [Guard, Approval, Constitution, and Sandbox](./07-security-governance/03-approval-constitution-sandbox.md) — `security-approval-sandbox` — `verified` — planned path: `07-security-governance/03-approval-constitution-sandbox.md`
- [OS Sandbox and Process Isolation](./07-security-governance/04-process-isolation.md) — `security-process-isolation` — `verified` — planned path: `07-security-governance/04-process-isolation.md`
- [Egress, Credentials, and Data Leakage](./07-security-governance/05-egress-and-credentials.md) — `security-egress-credentials` — `verified` — planned path: `07-security-governance/05-egress-and-credentials.md`
- [Trust for MCP, Skills, Plugins, and Hooks](./07-security-governance/06-extension-trust.md) — `security-extension-trust` — `verified` — planned path: `07-security-governance/06-extension-trust.md`
- [Fail-Closed Behavior and Platform Claims](./07-security-governance/07-fail-closed.md) — `security-fail-closed` — `verified` — planned path: `07-security-governance/07-fail-closed.md`

### Part 8: State and Observability

- [Why Durable State Is Required](./08-state-observability/01-why-durable-state.md) — `state-why-durable` — `verified` — planned path: `08-state-observability/01-why-durable-state.md`
- [SQLite, Event Logs, and Projections](./08-state-observability/02-sqlite-event-projection.md) — `state-sqlite-event-projection` — `verified` — planned path: `08-state-observability/02-sqlite-event-projection.md`
- [Sessions, Snapshots, CAS, and Workspace Journal](./08-state-observability/03-session-snapshot-journal.md) — `state-session-snapshot-journal` — `verified` — planned path: `08-state-observability/03-session-snapshot-journal.md`
- [Traces, Spans, Usage, and Cost](./08-state-observability/04-trace-usage-cost.md) — `state-trace-usage-cost` — `verified` — planned path: `08-state-observability/04-trace-usage-cost.md`
- [Diagnostics, Maturity, and Verification](./08-state-observability/05-diagnostics-and-verification.md) — `state-diagnostics-verification` — `verified` — planned path: `08-state-observability/05-diagnostics-and-verification.md`
- [Reconstructing a Failed Run](./08-state-observability/06-reconstructing-failures.md) — `state-reconstruct-failure` — `verified` — planned path: `08-state-observability/06-reconstructing-failures.md`

### Part 9: Tasks and Orchestration

- [Tasks, Workers, and Executors](./09-task-orchestration/01-task-worker-executor.md) — `task-worker-executor` — `verified` — planned path: `09-task-orchestration/01-task-worker-executor.md`
- [Leases, Heartbeats, Retries, and Idempotency](./09-task-orchestration/02-lease-heartbeat-retry.md) — `task-lease-retry` — `verified` — planned path: `09-task-orchestration/02-lease-heartbeat-retry.md`
- [Automation and Workflows](./09-task-orchestration/03-automation-and-workflow.md) — `task-automation-workflow` — `verified` — planned path: `09-task-orchestration/03-automation-and-workflow.md`
- [Checkpoints and Recovery](./09-task-orchestration/04-checkpoint-and-recovery.md) — `task-checkpoint-recovery` — `verified` — planned path: `09-task-orchestration/04-checkpoint-and-recovery.md`
- [Lanes, Fleets, and Scheduling](./09-task-orchestration/05-lane-fleet-scheduling.md) — `task-lane-fleet` — `verified` — planned path: `09-task-orchestration/05-lane-fleet-scheduling.md`
- [Subagents, Worktrees, and Topology](./09-task-orchestration/06-subagent-worktree-topology.md) — `task-subagent-worktree` — `verified` — planned path: `09-task-orchestration/06-subagent-worktree-topology.md`

### Part 10: Hosts and Protocols

- [CLI and Machine-Readable Output](./10-hosts-protocols/01-cli.md) — `host-cli` — `verified` — planned path: `10-hosts-protocols/01-cli.md`
- [TUI State Projection](./10-hosts-protocols/02-tui.md) — `host-tui` — `verified` — planned path: `10-hosts-protocols/02-tui.md`
- [ACP Stdio and Editor Interoperability](./10-hosts-protocols/04-acp.md) — `host-acp` — `verified` — planned path: `10-hosts-protocols/04-acp.md`
- [VS Code Context Bridge, Trust, and Compatibility](./10-hosts-protocols/06-vscode.md) — `host-vscode` — `verified` — planned path: `10-hosts-protocols/06-vscode.md`

### Part 11: Extension Ecosystem

- [Adding a Provider](./11-extension-ecosystem/01-adding-provider.md) — `extension-provider` — `verified` — planned path: `11-extension-ecosystem/01-adding-provider.md`
- [Adding a Governed Tool](./11-extension-ecosystem/02-adding-tool.md) — `extension-tool` — `verified` — planned path: `11-extension-ecosystem/02-adding-tool.md`
- [Integrating an MCP Server](./11-extension-ecosystem/03-integrating-mcp.md) — `extension-mcp` — `verified` — planned path: `11-extension-ecosystem/03-integrating-mcp.md`
- [Building Skills, Plugins, and Hooks](./11-extension-ecosystem/04-skill-plugin-hook.md) — `extension-skill-plugin-hook` — `verified` — planned path: `11-extension-ecosystem/04-skill-plugin-hook.md`
- [Adding a Host Without Duplicating Runtime](./11-extension-ecosystem/05-adding-host.md) — `extension-host` — `verified` — planned path: `11-extension-ecosystem/05-adding-host.md`
- [Extension Failure and Isolation](./11-extension-ecosystem/06-failure-isolation.md) — `extension-failure-isolation` — `verified` — planned path: `11-extension-ecosystem/06-failure-isolation.md`

### Part 12: Agent Engineering Practice

- [Hermetic Fixtures and Live-Provider Smoke](./12-engineering-practice/01-fixtures-and-smoke.md) — `practice-fixtures-smoke` — `verified` — planned path: `12-engineering-practice/01-fixtures-and-smoke.md`
- [Unit, Contract, Integration, and Electron Tests](./12-engineering-practice/02-test-layers.md) — `practice-test-layers` — `verified` — planned path: `12-engineering-practice/02-test-layers.md`
- [Concurrency Tests and Race Detection](./12-engineering-practice/03-concurrency-and-race.md) — `practice-concurrency-race` — `verified` — planned path: `12-engineering-practice/03-concurrency-and-race.md`
- [Benchmarks and Performance Budgets](./12-engineering-practice/04-benchmark-and-performance.md) — `practice-benchmark` — `verified` — planned path: `12-engineering-practice/04-benchmark-and-performance.md`
- [Cross-Platform Builds and Capability Probing](./12-engineering-practice/05-cross-platform.md) — `practice-cross-platform` — `verified` — planned path: `12-engineering-practice/05-cross-platform.md`
- [VSIX, SBOM, Provenance, and Release Evidence](./12-engineering-practice/06-release-evidence.md) — `practice-release-evidence` — `verified` — planned path: `12-engineering-practice/06-release-evidence.md`
- [Reading and Changing a Large Agent Codebase](./12-engineering-practice/07-reading-codebase.md) — `practice-reading-codebase` — `verified` — planned path: `12-engineering-practice/07-reading-codebase.md`

### Part 13: Hands-On Labs

- [Build and Trace the First Agent Turn](./13-hands-on-labs/01-first-agent-turn.md) — `lab-first-turn` — `verified` — planned path: `13-hands-on-labs/01-first-agent-turn.md`
- [Observe Streaming Events with a Fixture](./13-hands-on-labs/02-streaming-fixture.md) — `lab-streaming-fixture` — `verified` — planned path: `13-hands-on-labs/02-streaming-fixture.md`
- [Implement a Provider Adapter](./13-hands-on-labs/03-provider-adapter.md) — `lab-provider-adapter` — `verified` — planned path: `13-hands-on-labs/03-provider-adapter.md`
- [Implement a Governed Tool](./13-hands-on-labs/04-governed-tool.md) — `lab-governed-tool` — `verified` — planned path: `13-hands-on-labs/04-governed-tool.md`
- [Exercise Approval and Denial](./13-hands-on-labs/05-approval-and-denial.md) — `lab-approval-denial` — `verified` — planned path: `13-hands-on-labs/05-approval-and-denial.md`
- [Build a Recoverable Workflow](./13-hands-on-labs/06-recoverable-workflow.md) — `lab-recoverable-workflow` — `verified` — planned path: `13-hands-on-labs/06-recoverable-workflow.md`
- [Debug Worker Leases and Retries](./13-hands-on-labs/07-worker-lease-retry.md) — `lab-worker-retry` — `verified` — planned path: `13-hands-on-labs/07-worker-lease-retry.md`
- [Complete a VS Code Feature End to End](./13-hands-on-labs/08-vscode-feature.md) — `lab-vscode-feature` — `verified` — planned path: `13-hands-on-labs/08-vscode-feature.md`
- [Investigate a Failure from Traces](./13-hands-on-labs/09-trace-failure.md) — `lab-trace-failure` — `verified` — planned path: `13-hands-on-labs/09-trace-failure.md`
- [Design and Verify a New Agent Capability](./13-hands-on-labs/10-new-agent-capability.md) — `lab-new-capability` — `verified` — planned path: `13-hands-on-labs/10-new-agent-capability.md`

---

Do not edit this file directly. Change `docs/book/catalog.json` and run `python3 scripts/render-book-navigation.py`.
