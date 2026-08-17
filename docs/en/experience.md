# TUI and VS Code Experience Contract

[简体中文](../zh-CN/experience.md) | English

This document defines the shared interaction and presentation baseline for the
TUI and VS Code hosts. The machine-readable source is
[`experience-contract.json`](../../testdata/contracts/experience-contract.json). Host-specific
implementations may use native controls and vocabulary, but they must preserve
the semantics below.

## Information Architecture

Every primary surface has four logical regions:

1. **Context** identifies workspace, Chat, mode, trust, and Runtime.
2. **Transcript** contains requests, progressively disclosed reasoning,
   assistant output, tool activity, and Receipts.
3. **Action** contains the composer or the current approval, input, stop,
   retry, Setup, or Repair action.
4. **Detail** contains Edit Plans, changes, tasks, agents, jobs, usage, and
   diagnostics.

Context, current state, and the primary action survive compact layouts. Detail
collapses first. A wide layout may place Detail beside the Transcript, but must
not move the primary action unpredictably.

## State Language

Hosts project their implementation states onto seven canonical states:

| Canonical | Meaning | Typical TUI aliases | Typical VS Code aliases |
| --- | --- | --- | --- |
| `idle` | Ready for a new action, no work active | `idle` | `stopped` |
| `working` | Work or recovery is progressing | `typing`, `streaming`, `running` | `starting`, `recovering`, `running` |
| `waiting` | User approval or input is required | `pending`, `approval` | `approval_required`, `input_required` |
| `succeeded` | Verified or terminal success | `done`, `completed`, `ready` | `completed`, `ready`, `approve` |
| `degraded` | Usable with a stated capability gap | `degraded` | `degraded` |
| `failed` | A terminal operation failed or was denied | `failed`, `rejected`, `canceled` | `failed`, `deny`, `cancel` |
| `blocked` | Progress requires configuration, trust, or repair | `blocked` | `blocked`, `untrusted` |

UI copy should use a canonical label when practical. Protocol and persistence
values are not renamed by this contract. Every state needs visible text; color,
animation, or an icon may reinforce it but cannot replace it.

The user-facing lifecycle vocabulary is more specific than the canonical state
catalog: `Setup`, `Empty`, `Loading`, `Streaming`, `Approval`, `Verify`,
`Failure`, `Recovery`, and `Completed`. Each presentation includes a next
action. Their event coverage and cross-host invariants are defined in
[`host-journey-contract.json`](../../testdata/contracts/host-journey-contract.json).

## Visual Tokens

- Use host defaults for body and code typography.
- Use four spacing steps: inline, control, section, and panel.
- Use semantic roles `neutral`, `info`, `success`, `warning`, `danger`, and
  `focus`; do not assign product meaning to a raw color.
- TUI maps roles through `Theme` tokens. VS Code uses ThemeColor/Codicon and
  `--vscode-*` variables, including high-contrast themes.
- Icons accompany stable text labels. Glyph-only controls need an accessible
  name and tooltip.

## Terms

Use `Runtime`, `Chat`, `Turn`, `Edit Plan`, `Credential Reference`, and
`Receipt` consistently. Do not call a credential value a reference, and do not
present a generic success message as a Receipt unless it is backed by Runtime
verification or completion evidence.

## Consequential Actions

Read and navigation actions show scope but do not require confirmation.
Guarded execution, provider context transfer, and Edit Plan application must
show target, effect, exact Runtime identity, and explicit approve/deny actions.
Destructive or force-replace actions use danger treatment, state whether the
effect is reversible, and require explicit confirmation.

Dismissal is not approval. Approval remains bound to the displayed request and
Edit Plan ID. A failure keeps its reason, impact, and next action visible until
the user dismisses it or recovery succeeds.

## Motion and Accessibility

Full motion may signal ongoing work. Reduced mode replaces shimmer or repeated
animation with static state changes; still mode retains all information and
actions. Focus order is Context, Transcript, current Action, then Detail.
Keyboard access, stable accessible names, text status, light/dark/high-contrast
themes, reduced motion, and no-color operation are release requirements.

## Review Checklist

| ID | Review requirement |
| --- | --- |
| `UX-IA-01` | Context, Transcript, Action, and Detail ownership is unambiguous. |
| `UX-STATE-01` | Every displayed state maps to the canonical catalog and has text. |
| `UX-COLOR-01` | Color is semantic, theme-aware, and not the only signal. |
| `UX-KEYBOARD-01` | The complete primary journey is keyboard reachable. |
| `UX-FOCUS-01` | Focus order and restoration are deterministic. |
| `UX-DANGER-01` | Consequential actions show scope, identity, effect, and confirmation. |
| `UX-MOTION-01` | Reduced and still modes preserve meaning. |
| `UX-EMPTY-01` | Empty state explains the first available action. |
| `UX-LOADING-01` | Loading preserves context and exposes a stop or wait state. |
| `UX-FAILURE-01` | Failure shows reason, impact, and a repair or retry action. |
| `UX-RECEIPT-01` | Success claims distinguish output from verified Receipt evidence. |

Experience automation provides the required evidence for this checklist. New
UI work should cite the relevant IDs in tests or review notes. Host Journey
tests preserve deterministic evidence for Start, Stream, Approve, Input,
Cancel, Verify, Recover, and Receipt.

Run the deterministic baseline locally:

```bash
make experience-baseline
```

It checks the contract, TUI 80/120/160-column goldens, VS Code themes,
accessible labels, keyboard controls, and empty/loading/failure states. The
pinned Electron host gate is separate because it may download VS Code:

```bash
make experience-electron-baseline
```

Run the cross-Host journey contract with:

```bash
make host-journey-contract
```
