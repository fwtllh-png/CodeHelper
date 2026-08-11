# Documentation Governance

[简体中文](../zh-CN/documentation-governance.md) | English

CodeHelper treats documentation claims as maintained engineering contracts.
The book is governed by ownership, change impact, reproducible release facts,
freshness, and reader feedback rather than by occasional editorial cleanup.

## Sources of Truth

| File | Authority |
| --- | --- |
| `docs/book/catalog.json` | part order, chapter identity, language titles, and delivery status |
| `docs/book/schema/chapter.schema.json` | chapter Front Matter contract |
| `docs/book/governance.json` | owners, source domains, freshness SLA, release facts, screenshots, and link exclusions |
| `.github/CODEOWNERS` | generated GitHub review routing |
| Chapter Front Matter | chapter-specific code, test, and fact dependencies |

Change the registry first when ownership changes, then regenerate
`.github/CODEOWNERS` with:

```bash
python3 scripts/check-doc-governance.py codeowners
```

The generated output must match the tracked file exactly.

## Pull Request Impact Gate

The impact checker compares the PR base and head. It first maps changed paths
to chapter Front Matter; if no chapter declares a path, it falls back to the
owning source domain. A chapter counts as updated only when both its English
and Chinese files changed.

PR bodies use this machine-readable block:

```text
Documentation-impact: affected
Documentation-chapters: runtime-protocol, host-cli
Documentation-rationale: N/A
```

When observable facts do not change:

```text
Documentation-impact: none
Documentation-chapters: N/A
Documentation-rationale: Refactor preserves the protocol and CLI output.
```

The rationale is a review assertion, not a bypass from owner review.

Run the gate locally against a base revision:

```bash
BASE_REF=origin/main make doc-impact
```

## Fact and Release Gates

`make docs-check` validates links, bilingual mirrors, ownership, freshness,
the screenshot manifest, and governance unit tests. `make book-check` validates
the Catalog and all chapter metadata.

Before a release, run:

```bash
make release-fact-check
```

The commands in `governance.json` verify current CLI help/version behavior,
protocol schema drift, compatibility data, and both documentation contracts.
Add a fact command when a new release claim cannot be established by the
existing set.

## Freshness and Drift

Verified chapters have a 180-day maximum verification age and a 150-day
warning threshold. A weekly workflow performs strict source drift checks:
if a declared code, test, or source-of-truth path has a later Git date than
`last_verified`, the chapter must be rechecked and both language files updated.

Reconcile the book after a strict drift check with `make doc-reverify`:
chapters whose declared source paths changed since `last_verified` are
downgraded to `draft` in both language files and in the Catalog; chapters
whose sources are unchanged are re-stamped with today's date. Preview the
outcome with `make doc-reverify-dry-run` before applying it.

Screenshots are exceptional because they age poorly. Every book image must
appear in `governance.json` with its SHA-256 digest. Prefer Mermaid or text when
the same information can remain reviewable in source form.

External links are checked weekly rather than on every PR to avoid making
ordinary development depend on network availability. Stable intentional
exceptions require an explicit registry entry and owner review.

## Feedback and Cadence

Readers report incorrect facts, failed labs, missing prerequisites, translation
drift, and navigation friction through the documentation feedback issue form.
Maintainers classify each report as:

- factual drift: fix with the owning source change or within seven days;
- reproducibility or security defect: prioritize as a product defect;
- reading-order issue: review during the monthly navigation pass;
- enhancement: record against the relevant chapter or roadmap item.

The monthly pass reviews open documentation feedback, prerequisite edges, and
navigation order. The release pass closes fact failures and records any
platform-limited command. Ownership, impact, and freshness exceptions must
remain visible in tracked configuration or the PR record.
