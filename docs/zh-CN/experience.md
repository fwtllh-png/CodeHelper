# Web 与 TUI 体验契约

本文定义 Web 与 TUI Host 共同遵循的交互和呈现基线。机器可读的权威来源是
[`experience-contract.json`](../../testdata/contracts/experience-contract.json)。
Host 可以使用各自的原生
控件和表达方式，但必须保留以下语义。

## 信息架构

每个主要界面包含四个逻辑区域：

1. **Context**：标识 Workspace、Chat、Mode、Trust 和 Runtime。
2. **Transcript**：承载用户请求、渐进披露的 Reasoning、助手输出、Tool 活动和
   Receipt。
3. **Action**：承载 Composer，或当前 Approval、Input、Stop、Retry、Setup、Repair
   动作。
4. **Detail**：承载 Edit Plan、Changes、Task、Agent、Job、Usage 和 Diagnostic。

紧凑布局必须保留 Context、当前状态和主动作，Detail 最先折叠。宽布局可以把 Detail
放在 Transcript 旁边，但不能让主动作位置不可预测地变化。

## 状态语言

Host 将实现状态投影到七个 Canonical State：

| Canonical | 含义 | 常见 TUI Alias | 常见 Web Alias |
| --- | --- | --- | --- |
| `idle` | 可开始新动作，当前无任务 | `idle` | `stopped` |
| `working` | 工作或恢复正在进行 | `typing`、`streaming`、`running` | `starting`、`recovering`、`running` |
| `waiting` | 等待用户批准或输入 | `pending`、`approval` | `approval_required`、`input_required` |
| `succeeded` | 已验证或终态成功 | `done`、`completed`、`ready` | `completed`、`ready`、`approve` |
| `degraded` | 存在明确能力缺口但仍可使用 | `degraded` | `degraded` |
| `failed` | 操作终态失败或被拒绝 | `failed`、`rejected`、`canceled` | `failed`、`deny`、`cancel` |
| `blocked` | 必须先配置、授予信任或修复 | `blocked` | `blocked`、`untrusted` |

UI 文案应尽量使用 Canonical Label；本契约不重命名 Protocol 或持久化值。每个状态都
必须有可见文本，颜色、动效和图标只能辅助，不能代替文本。

面向用户的生命周期文案比 Canonical State 更具体，包括 `Setup`、`Empty`、`Loading`、
`Streaming`、`Approval`、`Verify`、`Failure`、`Recovery` 和 `Completed`，每种呈现
都必须包含下一步动作。事件覆盖和跨 Host 不变量定义在
[`host-journey-contract.json`](../../testdata/contracts/host-journey-contract.json)。

## 视觉 Token

- 正文与代码排版使用 Host 默认字体。
- 间距分为 Inline、Control、Section、Panel 四级。
- 只使用 `neutral`、`info`、`success`、`warning`、`danger`、`focus` 语义角色，不把
  产品含义绑定到原始色值。
- TUI 通过 `Theme` Token 映射；Web 使用 `--ch-*` CSS Token，并支持 Light、Dark
  与 Reduced Motion。
- 图标与稳定文本标签一起使用；纯 Glyph 控件必须有 Accessible Name 和 Tooltip。

## 术语

统一使用 `Runtime`、`Chat`、`Turn`、`Edit Plan`、`Credential Reference` 和
`Receipt`。不能把 Credential Value 称为 Reference；没有 Runtime 验证或完成证据时，
不能把普通成功消息称为 Receipt。

## 重要操作

读取和导航动作需要展示 Scope，但无需确认。受治理执行、向 Provider 传输 Context、
应用 Edit Plan 时，必须展示 Target、Effect、准确 Runtime Identity，以及明确的
Approve/Deny 动作。删除、强制覆盖等破坏性动作使用 Danger 样式，说明是否可恢复，并
要求显式确认。

关闭界面不等于批准。Approval 必须继续绑定到已展示的 Request 与 Edit Plan ID。失败
原因、影响和下一步动作应持续可见，直到用户关闭或恢复成功。

## 动效与可访问性

Full Motion 可提示进行中的工作；Reduced 模式以静态状态变化代替 Shimmer 或重复动效；
Still 模式仍保留全部信息和动作。Focus 顺序为 Context、Transcript、当前 Action、
Detail。键盘访问、稳定 Accessible Name、文本状态、Light/Dark/High Contrast Theme、
Reduced Motion 和 No-color 模式都是发布要求。

## Review Checklist

| ID | 检查要求 |
| --- | --- |
| `UX-IA-01` | Context、Transcript、Action、Detail 的职责明确。 |
| `UX-STATE-01` | 每个显示状态映射到 Canonical Catalog，并带文本。 |
| `UX-COLOR-01` | 颜色具有语义、跟随主题且不是唯一信号。 |
| `UX-KEYBOARD-01` | 完整主路径可通过键盘完成。 |
| `UX-FOCUS-01` | Focus 顺序与恢复行为确定。 |
| `UX-DANGER-01` | 重要操作展示 Scope、Identity、Effect 和确认。 |
| `UX-MOTION-01` | Reduced 与 Still 模式保留全部语义。 |
| `UX-EMPTY-01` | Empty State 说明第一个可用动作。 |
| `UX-LOADING-01` | Loading 保留 Context，并展示 Stop 或 Wait 状态。 |
| `UX-FAILURE-01` | Failure 展示原因、影响和 Repair 或 Retry 动作。 |
| `UX-RECEIPT-01` | 成功声明区分普通输出与已验证 Receipt 证据。 |

Experience 自动化为本 Checklist 提供证据。新增 UI 应在测试或 Review 记录中引用对应
ID。Host Journey Test 为 Start、Stream、Approve、Input、Cancel、Verify、Recover 和
Receipt 保留确定性证据。

本地执行确定性基线：

```bash
make experience-baseline
```

该门禁检查契约、TUI 80/120/160 列 Golden，以及 Web Theme、Accessible Label、
键盘控件和 Empty/Loading/Failure State。

跨 Host Journey Contract 命令：

```bash
make host-journey-contract
```
