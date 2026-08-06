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
source_of_truth:
  - scripts/package-release.sh
  - extensions/vscode/scripts/release/vscode-matrix.mjs
status: verified
last_verified: 2026-08-06
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

## Verification

```bash
make package
make vscode-release-dry-run
```

## Review Questions

1. What does SBOM prove and not prove?
2. Why separate universal and target VSIX?
3. What makes provenance publishable?
4. How do signature, provenance, and SBOM claims differ?
5. Why should channel promotion reuse artifact digests?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `practice-release-evidence` |
| Status | `verified` |
| Last verified | 2026-08-06 |
