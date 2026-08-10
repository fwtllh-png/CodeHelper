---
id: security-egress-credentials
title: Egress、Credential 与数据泄漏
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - model-credential-lifecycle
  - security-threat-model
code_paths:
  - internal/security/egress
  - internal/security/keyring
  - internal/adapter/provider/httpclient
test_paths:
  - internal/security/egress/gate_test.go
  - internal/security/keyring/store_test.go
  - internal/adapter/provider/httpclient/credentials_test.go
source_of_truth:
  - docs/zh-CN/security.md
  - internal/security/egress/gate.go
status: draft
last_verified: null
---

# Egress、Credential 与数据泄漏

简体中文 | [English](../../en/07-security-governance/05-egress-and-credentials.md)

## 学习目标

追踪 Secret Reference 到 Governed HTTP Header，理解 Host-scoped Egress Approval，并
识别 Model/Log/Process Leakage Channel。

## Secret Lifecycle

Tracked Config 保存 `CredentialRef` 而非 Value。Value 在 HTTP 构造前，从 Explicit Env、
OS Keyring 或 Constrained Resolver Source 解析：

```text
reference -> request-scoped resolve -> authorization header
          -> governed client -> discard
```

Raw Value 不得进入 Prompt、ModelRequest JSON、Event、Receipt、Trace、Command Argument、
Diagnostic Dump、Child Environment。Redaction 是 Defense in Depth，不是记录许可。

## Egress Gate

Enforcing `egress.Gate` 是 Session-scoped Normalized Protocol+Host Allowlist。Wrapped
Transport 检查每次 Request；Redirect 会重新进入 Transport，因此 New Host 需要新 Authority。

对 `https://api.example` 的 Approval 不覆盖 Other Scheme、Arbitrary Subdomain、Redirect
Destination 或绕过 Governed Client 创建的 Raw Socket。Provider Endpoint、MCP/Web
Backend 都是 Security-sensitive Input。

## Leakage Channel

| Channel | Control |
| --- | --- |
| Model Context/Tool Result | Source Budget、No Secret Source、Bounded Output |
| HTTP/Redirect | Resolver、Egress Gate、Safe Redirect |
| Process Environment | Allowlist/Sanitization |
| Log/Error/Dump | Structured Redaction、Bounded Message |
| Memory/Snapshot | Explicit Write、Secret Heuristic、Retention |
| Local Service | Loopback Default、External Auth Gateway |

Native Search/Remote Content 在取回后仍不可信。TLS 保护到 Host 的 Transport，不赋予返回
Instruction Authority。

## Incident Response

停止受影响 Execution/Distribution，Revoke/Rotate Credential，保留 Sanitized Evidence，
确定 Event/Receipt/Log Scope，使 Artifact 失效，修复 Control 并补 Regression。不得把
Leaked Value 粘贴进 Issue。

## 验证

```bash
go test ./internal/security/egress ./internal/security/keyring
go test ./internal/adapter/provider/httpclient -run 'Test.*Credential'
make secret-leak-test
```

## 复习问题

1. Redirect 为什么必须重新检查？
2. 哪些 Data Structure 只能含 Reference？
3. TLS 为什么不能解决 Remote Content Prompt Injection？

## 延伸阅读

- [Credential Reference 与 Secret Lifecycle](../04-model-and-provider/05-credential-lifecycle.md)
