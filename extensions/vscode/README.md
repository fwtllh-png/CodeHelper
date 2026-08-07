# CodeHelper for VS Code

VS Code companion for the CodeHelper Runtime.

The extension supports local multi-root workspaces with one Runtime per root
and a separate durable binding, data directory, and projection namespace for
each root (maximum eight).
Chat has an explicit root selector; editor commands route by the document's
workspace root. Changes and background views group state by root.

The Session rail uses a bounded virtual DOM window for large histories.
Hidden Chat views keep the Runtime projection current without posting DOM
snapshots, then render the latest projection when shown. Release gates cover
200-Turn switching, 1000-Session search, first-interactive latency, Light,
Dark, High Contrast, approximately 200% zoom, and local multi-root recovery.

It also supports Chat, explicit `@file` / `@selection` context, approval-bound native
diff previews, and workspace-scoped Threads, Agents, Tasks, Jobs, Approvals, and
Usage views.

The extension supports `auto`, `external`, `managed`, and `bundled` binary
sources. External binaries come from an absolute `codehelper.binaryPath` or the
local Extension Host `PATH`. Managed and bundled binaries require the
target-specific artifact to match the built-in Ed25519-signed release manifest.
Use `CodeHelper: Check for Binary Updates` for an explicit managed install.

Build and verify the installable package from the repository root with
`make vscode-package`. The universal VSIX intentionally does not bundle the
Runtime executable; target-specific bundles are produced by the release flow.
`make vscode-release-dry-run` creates the universal and five target VSIX files,
SBOM, provenance, checksums, and non-uploading plans for Marketplace, Open VSX,
enterprise, and offline distribution. Dry-run artifacts use a temporary signing
key and must not be published.

`make vscode-rc` produces the machine-readable compatibility and RC reports.
A dirty worktree or temporary dry-run signing key is reported as
`publishable=false`; it is never presented as a public release candidate.

The extension runs in the local UI Extension Host and accepts only local
`file:` workspaces. Remote SSH, Dev Containers, Codespaces, and other
`vscode-remote:` workspaces are not supported.
