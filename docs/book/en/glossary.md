# Agent Engineering Glossary

[简体中文](../zh-CN/glossary.md) | English

This glossary defines the preferred vocabulary for the book. Code identifiers,
protocol names, and CLI flags remain in their source form.

| Term | Definition | Usage note |
| --- | --- | --- |
| Agent | A system that uses a model, context, tools, and state to pursue a goal across one or more steps. | Do not use as a synonym for the model alone. |
| Agent loop | The controlled cycle of model inference, tool requests, tool results, and continuation. | In CodeHelper this belongs in `internal/runtime/agent`. |
| Approval | A human decision authorizing or denying a consequential action. | Approval does not replace hard security controls. |
| Automation | A durable rule that creates work when a trigger or schedule matches. | Distinguish from an interactive Agent turn. |
| Capability | A declared feature supported by a model, provider, runtime, or host. | Examples include streaming and tool calls. |
| Catalog | A structured registry used to discover models, tools, or book chapters. | State which catalog is meant. |
| Checkpoint | Durable progress information from which execution can continue. | A checkpoint is not necessarily a complete snapshot. |
| Constitution | Non-bypassable runtime rules that constrain tool execution. | Keep the CodeHelper type name in English. |
| Context | Information made available to the model for the current inference. | Distinguish from persistent state and model training data. |
| Context compaction | Reducing context size while preserving information needed for future work. | Explain the information-loss tradeoff. |
| Credential reference | A non-secret pointer to an environment variable, protected file, or OS keyring entry. | TOML stores the reference, not the secret. |
| Event | An immutable runtime fact emitted while an operation progresses. | Distinguish from current projected state. |
| Fail closed | Refusing an action when the required security decision or capability cannot be established. | Do not describe silent degradation as fail-closed. |
| Fixture | Deterministic recorded input/output used to exercise the real runtime without a live dependency. | Prefer “hermetic fixture” when no network is used. |
| Fleet | Coordinated scheduling across multiple lanes or execution resources. | Use the CodeHelper domain meaning. |
| Guard | The boundary that evaluates tools against policy, permissions, approval, constitution, journal, and sandbox requirements. | A host must not bypass it. |
| Host | A user or client-facing adapter that submits operations and projects events. | CLI, TUI, HTTP, ACP, Web, and VS Code are hosts. |
| Idempotency | The property that retrying an operation does not create unintended duplicate effects. | State the idempotency key or boundary. |
| Journal | A durable record of intended and completed workspace effects used for evidence and recovery. | Not a general application log. |
| Lane | An ordered execution boundary used to coordinate related work. | Distinguish from an OS thread. |
| Lease | Time-bounded ownership of durable work by a worker. | Usually paired with heartbeat and takeover rules. |
| MCP | Model Context Protocol, used to expose tools and resources through an external server. | Spell out on first use in a chapter. |
| Model | The inference system addressed by a model identifier or wire ID. | Do not conflate model and provider. |
| Operation | A request submitted to the runtime with identity, input, mode, and execution options. | Events and receipts belong to an operation. |
| Permission posture | The strategy for handling tool risk: for example `never`, `suggest`, `auto`, or `bypass`. | Hard controls still apply in `bypass`. |
| Plugin | A packaged extension loaded through a governed extension boundary. | Explain trust and lifecycle. |
| Policy | Configurable rules that allow, deny, or require approval for an action. | Distinguish from constitution. |
| Projection | Current state derived by applying ordered events. | A projection can be rebuilt from durable facts. |
| Provider | The service or adapter that transports model requests and responses. | One provider may expose multiple models. |
| Receipt | Structured evidence describing the outcome and effects of execution. | Do not use as a synonym for an event. |
| ReAct | A pattern that alternates reasoning and action/tool use. | Present as a pattern, not a complete runtime architecture. |
| Recovery | Restoring a valid execution state after interruption or failure. | State which durable facts are used. |
| Runtime | The shared system that owns operations, Agent loops, tools, state, governance, and events. | Hosts are clients of the runtime. |
| Sandbox | OS-backed isolation that constrains processes, files, and other resources. | Do not claim a strong sandbox when unavailable. |
| Session | Durable conversational or execution continuity across operations. | Distinguish from one process lifetime. |
| Skill | Instructional or procedural capability loaded through the extension system. | A skill does not bypass runtime governance. |
| Snapshot | A materialized state image used to speed restore or inspection. | Explain consistency with the event source. |
| Span | A timed unit of traced work nested within a trace. | Use for observability, not product state. |
| Task | A durable unit of executable work with lifecycle, attempts, and ownership. | A task record without an executor is not executable. |
| Tool | A typed capability the model may request to inspect or affect the environment. | Consequential tools pass through Guard. |
| Trace | An end-to-end observability record linking related spans and runtime identity. | Avoid embedding secrets or raw sensitive data. |
| Turn | One user-to-Agent interaction that may include multiple model and tool steps. | Distinguish from a single inference request. |
| Verification | Evidence-producing checks that test whether requested work satisfies its acceptance conditions. | A successful tool call is not verification by itself. |
| Wire ID | The provider-facing model identifier sent over the transport protocol. | It may differ from the catalog model ID. |
| Worker | A process or loop that claims and executes durable tasks. | Ownership is governed by lease and heartbeat. |
| Workflow | A durable multi-step graph with dependencies, checkpoints, and recovery semantics. | Distinguish from a single Agent loop. |
| Workspace | The repository or directory boundary in which CodeHelper operates. | Identity and trust are scoped to it. |

## Translation and Style

- Keep public type names, package paths, protocol fields, commands, and flags in
  their original form.
- Introduce an acronym in full on first use.
- Prefer one term for one concept; add an entry before inventing a synonym.
- When an external specification owns a term, follow that specification and
  link it from the relevant chapter.
- The Chinese glossary is authoritative for preferred translations; both files
  must be updated together.
