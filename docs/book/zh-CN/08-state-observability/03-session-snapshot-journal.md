---
id: state-session-snapshot-journal
title: Session、Snapshot、CAS 与 Workspace Journal
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - state-sqlite-event-projection
  - context-memory-snapshot
code_paths:
  - internal/persist/session
  - internal/persist/snapshot
  - internal/persist/state/cas
  - internal/persist/workspacejournal
test_paths:
  - internal/persist/snapshot/repository_test.go
  - internal/persist/state/cas/store_test.go
  - internal/persist/workspacejournal/recover_test.go
source_of_truth:
  - internal/persist/snapshot/repository.go
  - internal/persist/workspacejournal/journal.go
status: verified
last_verified: 2026-08-06
---

# Session、Snapshot、CAS 与 Workspace Journal

简体中文 | [English](../../en/08-state-observability/03-session-snapshot-journal.md)

## 学习目标

区分 Session Metadata、Snapshot Checkpoint、Immutable Content Store 与 Workspace
Side-effect Recovery。

| Store | Identity | Purpose |
| --- | --- | --- |
| Session Repository | Session/Workspace ID | Lifecycle/List |
| Snapshot Repository | Snapshot/Thread/Sequence | Fast Restore |
| CAS | Content Hash | Deduplicated Immutable Bytes |
| Workspace Journal | Turn/Path Fingerprint | Rollback/Crash Recovery |

```mermaid
flowchart TD
    S[Session] --> N[Snapshot Metadata]
    N --> C[CAS Payload]
    T[Tool Write] --> J[Before-image Journal]
    C --> R[Reconstruction]
    J --> R
```

Snapshot 带 Schema、Sequence、Content Handle、Size 与 Digest。Save 在提交 Metadata 前
Retain CAS；Read 验证 Hash/Schema。CAS 验证 ID-content 一致性、Regular-file Layout、
Reference Metadata、Atomic Write 与 Cross-process Lock。

Journal 记录 Turn 内每个 Path 的 First Before-image 与 After Fingerprint；Durable
Ledger 先于对应 Workspace Write 落盘。Recovery 跳过 Live Owner，只在 Current State
匹配时恢复 Abandoned Work，并保留 Conflict 供重试。

## Commit/Reference Window

Snapshot Save 先保证 Content，再提交 Metadata：

```text
normalize payload -> compute/verify content id -> cas put/retain
                  -> sqlite snapshot metadata commit
```

Retain 后 Metadata 前失败可能留下 Reclaimable Content；反向排序会产生指向缺失内容的
Committed Snapshot。CAS Reference Change 使用 Cross-process Lock/Atomic Replace。

Journal 是 Side-effect 对应协议：

```text
durable owner/before-image -> workspace write -> after fingerprint -> turn commit
```

两者都宁可留下 Recoverable Leftover，也不创建 Dangling Authoritative Reference。

## Identity Boundary

Session 标识 Workspace Lifecycle；Thread/Turn 标识 Causal History；Snapshot Sequence
标识 Reconstruction Point；CAS ID 标识 Byte；Journal Turn/Path 标识 Effect。分离这些
Identity 可防止 Cross-workspace Restore/Incorrect Rollback。

## 设计取舍

大 Payload 放 SQLite 会增加 Churn；CAS 分离 Immutable Byte，SQLite 管 Reference。
Git 无法覆盖 Untracked File、Partial Turn 和 Non-Git Workspace，因此 Journal 仍必要。

## 失败与安全边界

- Snapshot Corruption/Schema Mismatch 被拒绝。
- CAS Tampering、Symlink、Invalid ID/Reference Fail Closed。
- Last-reference Release 删除内容。
- Before-image Failure 阻止 Edit。
- Recovery 不覆盖 External Edit。

## 测试与验证

```bash
go test ./internal/persist/session ./internal/persist/snapshot
go test ./internal/persist/state/cas ./internal/persist/workspacejournal
```

## 动手实验

追踪 Snapshot Save 的 CAS Retain/Metadata Commit，再与 Journal Before-image 比较，列出
两者各自能恢复什么。

## 复习问题

1. Snapshot Metadata 为什么与 CAS Byte 分离？
2. Git 为什么不足以承担 Turn Journal？
3. 自动 Rollback 的前提是什么？
4. CAS Retain 为什么先于 Snapshot Metadata Commit？
5. 哪些 Identity 防止 Cross-workspace Recovery Mistake？

## 延伸阅读

- [从失败运行还原系统行为](./06-reconstructing-failures.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `state-session-snapshot-journal` |
| 状态 | `verified` |
| 最后验证 | 2026-08-06 |
