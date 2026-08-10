---
id: practice-release-evidence
title: VSIX, SBOM, Provenance, and Release Evidence
audience:
  - contributor
  - operator
prerequisites:
  - practice-cross-platform
  - host-vscode
code_paths:
  - scripts
  - extensions/vscode/scripts/release
test_paths:
  - extensions/vscode/src/binary/manifest.test.ts
  - extensions/vscode/src/binary/store.test.ts
  - extensions/vscode/src/test/suite/index.ts
source_of_truth:
  - scripts/package-release.sh
  - extensions/vscode/scripts/release/vscode-matrix.mjs
  - extensions/vscode/scripts/matrix/journeys.mjs
  - extensions/vscode/RELEASE-EVIDENCE.md
status: draft
last_verified: null
---

# VSIX, SBOM, Provenance, and Release Evidence

English | [简体中文](../../zh-CN/12-engineering-practice/06-release-evidence.md)

## Learning Objectives

Understand how build artifacts become inspectable release evidence rather than
an unverified archive.

Core packaging builds target binaries, checksums, CycloneDX SBOM, manifest, and
install/upgrade/rollback smoke. VS Code release creates universal and five
target VSIX artifacts, validates exact contents, records source identity,
digests, SBOM, provenance, performance matrix, channel dry-run, and RC report.
`RELEASE-EVIDENCE.md` and the machine-readable Journey manifest bind product
acceptance paths to automated or reproducible manual evidence.

```mermaid
flowchart LR
    S[Source identity] --> B[Reproducible builds]
    B --> D[Digests / SBOM]
    D --> V[VSIX/package validation]
    V --> P[Provenance + smoke]
    P --> R[Release candidate report]
```

Universal VSIX contains no binary; target VSIX contains exactly its target.
Signed manifests bind target/version/digest and support authorized key rotation
and revocation. Dirty source or dry-run signing is recorded and cannot become
a publishable claim.

## Evidence Chain

```text
source revision + clean/dirty state
 -> deterministic build command/toolchain
 -> artifact bytes + target identity
 -> SHA-256 checksums + SBOM
 -> signed release/update manifest
 -> install/handshake/upgrade/rollback smoke
 -> provenance and release-candidate report
 -> publication record
```

Each link names the previous digest or identity. Provenance describes how an
artifact was produced; a signature authorizes exact bytes/metadata; SBOM lists
components. None alone proves source correctness, absence of vulnerabilities,
or successful installation.

## Journey and Matrix Evidence

The VS Code Matrix requires seven job records and a complete Journey set.
Electron evidence contributes automated Journey IDs; the report rejects any
missing ID. The retained Panel-move Journey is manual because VS Code owns view
container movement, but its reproduction steps are packaged in the VSIX and
must be present for RC.

Current closure is 20/20 Journeys: 19 automated and one documented manual.
Coverage includes local single-root/multi-root, ARM64/Rosetta x64, native
context/navigation, accessibility, incremental Transcript, hidden resume,
Retry/Continue, Session lifecycle/search, and Plan destinations. There is no
Remote SSH, Dev Container, Codespaces, or Full Editor Chat release claim.

RC verifies Matrix completeness, Journey completeness, compatibility,
performance field presence and budgets, dependency audit, secret scan, VSIX
allowlist, SBOM, provenance, signatures, and checksums. A validated dry run
still records `publishable=false` and `uploaded=false`.

## Reproducibility and Promotion

`-trimpath`, fixed version metadata, controlled dependencies, stable archive
layout, and isolated output reduce accidental variation. Rebuilding should
either match bytes or explain signed, reviewed nondeterminism.

Promotion reuses verified artifact digests; it does not rebuild “the same”
version for another channel. Dry-run keys, dirty sources, fallback SBOMs, or
missing native matrix evidence remain explicitly non-publishable.

Rollback verifies the previous artifact and compatibility before activation.
Revocation blocks known-bad manifests even when cached locally.

## Failure Boundaries

- Forbidden/missing VSIX files fail packaging.
- Artifact digest must match provenance.
- Unsafe URL, replay, revocation, and unknown signing key fail closed.
- Release smoke uses fake credentials and bounded temp state.
- External signing/publishing is never inferred from a dry run.
- Missing automated Journey or undocumented manual Journey fails RC.
- Source changes after provenance generation invalidate the RC report.

## Verification

```bash
make package
make vscode-release-dry-run
make vscode-rc
```

## Review Questions

1. What does SBOM prove and not prove?
2. Why separate universal and target VSIX?
3. What makes provenance publishable?
4. How do signature, provenance, and SBOM claims differ?
5. Why should channel promotion reuse artifact digests?
6. Why does a source edit require regenerating provenance before RC?
7. Why is the Panel-move Journey documented rather than falsely automated?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `practice-release-evidence` |
| Status | `verified` |
| Last verified | 2026-08-07 |
