---
id: chapter-id
title: Chapter Title
audience:
  - learner
  - contributor
prerequisites:
code_paths:
test_paths:
source_of_truth:
  - path/to/authoritative-artifact
status: draft
last_verified: null
---

# Chapter Title

English | Chinese mirror: `docs/book/zh-CN/<part>/<chapter>.md`

> Drafting note: copy this template to the path declared by
> `docs/book/catalog.json`, replace every placeholder, and keep the English and
> Chinese chapters in the same change.

## Learning Objectives

After this chapter, the reader can:

- explain the problem and the relevant terminology;
- follow the CodeHelper execution path in source;
- reproduce the main behavior and one failure mode.

## Prerequisites

List required chapters, tools, and platform capabilities.

## Problem Background

Explain the general Agent-engineering problem before introducing CodeHelper.

## Core Concepts

Define terms and distinguish concepts that are commonly confused.

## CodeHelper Design

Describe current behavior, ownership boundaries, invariants, and why this
design was chosen.

## Execution Flow

Use prose first. Add a focused Mermaid diagram when it improves understanding.

## Code Map

| Concern | Source | Why it matters |
| --- | --- | --- |
| Primary implementation | `path/to/package` | Replace this row |
| Contract or schema | `path/to/contract` | Replace this row |
| Tests | `path/to/tests` | Replace this row |

## Implementation Walkthrough

Walk through the smallest set of types and functions needed to understand the
design. Link to source instead of copying large blocks.

## Tradeoffs and Alternatives

State constraints, alternatives, and consequences.

## Failure Modes and Security Boundaries

Cover at least one failure path and identify attacker-controlled input,
fail-closed behavior, cleanup, and platform limitations where relevant.

## Tests and Verification

```bash
# Replace with commands that exist and were actually run.
go test ./path/to/package
```

Record expected results and environmental limits. Do not mark the chapter
`verified` until these commands have been executed.

## Hands-On Lab

### Goal

State the observable outcome.

### Steps

1. Prepare a fixture or isolated workspace.
2. Run the behavior.
3. Inspect evidence.

### Expected Result

Describe what proves success.

### Cleanup

Describe how to remove generated state.

## Review Questions

1. Which invariant is most important in this design?
2. What failure would occur if the ownership boundary were bypassed?
3. Which test is the strongest evidence for the behavior?

## Further Reading

- Link primary specifications or authoritative external material.

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `chapter-id` |
| Status | `draft` |
| Last verified | Not yet verified |
