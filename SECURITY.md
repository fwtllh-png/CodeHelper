# 安全政策

## 支持版本

QCode 目前处于初始开发阶段。安全修复应用于最新标签版本和 `main` 分支。
除非 Release Notes 另有说明，不维护更早的预发布版本。

| 版本 | 是否支持 |
| --- | --- |
| 最新标签版本 | 是 |
| `main` | 是，开发分支 |
| 更早的预发布版本 | 否 |

## 报告安全漏洞

请通过
[GitHub Security Advisories](https://github.com/fwtllh-png/QCode/security/advisories/new)
私下报告安全漏洞。不要为疑似漏洞创建公开 Issue。

报告只应包含复现和评估问题所需的信息：

- 受影响版本或 Commit；
- 操作系统和 Sandbox Backend；
- 受影响的 Host、Provider、Tool 或 Protocol；
- 最小复现步骤；
- 预期和实际安全边界；
- 潜在影响；
- 可用时提供建议修复方式。

不要提交凭证、私有源码、生产数据或其他 Secret。应尽可能使用合成 Fixture。

维护者目标是在三个工作日内确认收到报告，并在七个工作日内给出初步评估。
复杂问题可能需要更多时间。理解影响范围和修复路径后，维护者会与报告者协商
披露时间。

## 安全范围

安全敏感区域包括：

- Tool Guard、Policy、Approval、Permission 和 Constitution Enforcement；
- Workspace 文件边界、Edit Journal 和 Recovery；
- Process Execution 和 OS Sandbox Isolation；
- Credential Reference、Redaction 和 Log；
- Provider、MCP、Plugin、Hook 和 Network Boundary；
- localhost HTTP/WebSocket 与 Web Trust Boundary；
- 持久化 Session、Event、Snapshot 和 Trace；
- Release Artifact、Checksum、Update 和 Supply-chain Metadata。

没有明确安全影响的一般加固建议，可以通过普通 Issue 或 Discussion 提交。
