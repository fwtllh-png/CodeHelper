# RFC-011：Egress Broker 与 Keyring

> 状态：**T1–T3 已落地**（会话级出网 Gate + OS keyring + egress deny 审批重试）；§8.6 其余条目后置
> 关联：[ROADMAP §8.6](../ROADMAP.zh-CN.md)、[RFC-010 ModelRouting](./RFC-010-model-routing.zh-CN.md)（T4 probe 依赖本 RFC 的 broker）
> 影响面：`internal/security/egress`、`internal/security/keyring`、`internal/adapter/provider/httpclient`、`internal/adapter/tool/web`、`internal/adapter/tool/guard`、`internal/runtime/app/wire`、`internal/host/cli`

---

## 1. 问题

§8.6 写明「provider/web 通过专用 broker 出网」与「执行期网络 enforcement」。现状是：

1. **provider 裸 Dial。** `httpclient.New()` 使用默认 `http.Client`，会话选定的 endpoint 之外没有任何闸门。
2. **web 只问不拦。** Guard 对 `CapabilityNetwork` 做 Immediate host 审批，批准后工具自己 `new http.Client` 出网——审批与 Dial 脱节，重定向或二次请求可以碰到未批准的 host。
3. **keyring 配置面先行。** `kind = "keyring"` 在 config / catalog / `codehelper auth` 都合法，但 `httpclient.Keyring` 无生产实现，解析时报 `credential keyring is not configured`。
4. **RFC-010 T4（capability probe）被挡住。** probe 是出网行为（RFC-010 C5），没有 broker 就不能安全地做。

---

## 2. 硬约束

### C1：生产路径必须 Enforce；单测默认不碎

`httpclient.New()` 在 Gate 为 nil 时保持今天的开放行为，避免每个 httptest 单测都要 Grant。`wire` 与一切 CLI 出网命令必须挂上 Enforce Gate。

### C2：可信配置选的 endpoint 自动放行

会话启动时对 `RouteSet` 里每个已解析用途的 endpoint host **自动 Grant**。否则每条 `codehelper exec` 都会在第一轮采样前失败。fixture / loopback 同样 Grant 实际 server host。已配置的 web search backend URL 同理（env/config 选的后端）。

### C3：web_fetch 仍要 Guard 审批，且 Dial 执法

自动 Grant 不含任意用户 URL。`web_fetch` / `web_scrape` 仍走 Immediate ask；批准后写入同一 Gate；工具 HTTP 经 `WrapClient`，未 Grant 的 host（含重定向目标）RoundTrip 失败。

### C4：不强迫离开 env 凭据

OS keyring 是 `kind=keyring` 的运行面补齐，不是把所有人迁出 `env` / `file`。

### C5：拒绝错误可识别

egress deny 的错误字符串稳定，供 CLI / 将来的 probe 区分「未批准」与传输故障。

---

## 3. 设计

### D1：`egress.Gate`

会话级 allowlist：`(host, protocol)`。`Allow` / `Allowed` / `WrapClient` / `RoundTripper`。host 规范化（小写）；空 Gate 在 Enforce 模式下拒绝一切。

### D2：接线

- provider：`httpclient.Client.Egress`；wire 注入 Gate 并 Grant routes。
- web：`Options.HTTP` 为经 Gate wrap 的 client；无则退回今天的临时 client（仅测试）。
- Guard：`OnNetworkAllow(host, protocol)`；在 cacheApproval 与 ActionAllow 路径上对 invocation 的 host 资源调用。
- `codehelper model list --live`：解析 provider endpoint → Grant → WrapClient，禁止裸 Dial。

### D3：Keyring

`zalando/go-keyring` 实现 `httpclient.Keyring`，service 名固定 `codehelper`。`DefaultCredentials()` 接上。`auth login --kind keyring --from-env VAR` 写入系统商店；`logout` 删除对应条目（若存在）。

---

## 4. 分片

| 分片 | 内容 | 判据 |
| --- | --- | --- |
| T1 | Gate + provider/web/Guard/live-list 接线 | **已落地**：未 Grant 失败；已 Grant 的 openai / fixture 照常；web 未批准不能 Dial |
| T2 | OS keyring + auth 读写 | **已落地**：`kind=keyring` 不再报 not configured |
| T3 | egress deny → 审批 → Grant → 重试 | **已落地**：`error_category=egress_denied`；Guard 对未 Grant host 弹 `network_host`；allow 后重试一次；连通性失败不走审批 |

---

## 5. 明确不做

- 默认把 sandbox `AllowNetwork` 改 false（§8.6 #1 全量）
- shell managed-egress Dial 拦截（`NetworkDeferred`）
- 文件读前 secret detection/redaction
- MCP/Plugin/Skill 签名、SBOM、版本锁
- 网页/MCP/terminal 不可信来源标签
- Windows restricted token（继续 fail-closed 现状）
- RFC-010 T4 `model probe`（已落地，见 RFC-010 §15）

- 强制全员改用 keyring
- 用审批「绕过」真实超时 / 拒连 / DNS 失败（只处理策略拒绝）

---

## 6. 验收

- wire 会话对 act（及槽位）endpoint 可采样；对未 Grant 的其它 host，provider RoundTrip 报 egress denied
- `web_fetch` 未批准时 Dial 不到；批准后到同一 host；重定向到未批准 host 失败
- `auth login --kind keyring --from-env ...` 后 Resolve 成功（有系统后端时）；无 Keyring 注入时错误仍不泄露 secret
- `model list --live` 经 Gate
- web 工具在 Gate 拒绝时结果分类为 `egress_denied`（含 host）；Guard 发起 host 审批，allow 后 Grant 并重试成功；deny 则把原失败交回模型

---

## 7. T3 实施结果与偏离

落地面：`adapter/tool/web`（分类 + `web_search` backend 资源）、`adapter/tool/guard`（执行中 egress 审批重试）、TUI 审批文案。

### 已落地

- **分类。** Dial/`RoundTrip` 命中 `egress.ErrDenied` 时 `error_category=egress_denied`，文案 `egress denied · host=…`，不再与普通 `network` 混在一起。
- **Guard 重试。** 工具返回 egress deny 后弹 `network_host`；allow/session/always 经 `OnNetworkAllow` Grant 后同调用再 Execute 一次；deny/cancel/expired 软返回原失败结果。
- **`web_search` 资源。** Descriptor 列出配置的 backend endpoint（url/ID），不再把 `query` 当网络目标。

### 偏离

1. **只处理策略拒绝。** 超时与拒连仍直接失败；审批不能假装打通不可达的 `cn.bing.com`。
2. **同一 call 只重试一次。** 避免审批死循环；第二次仍 deny 则交回模型。
3. **`bypass`（Full）对执行中新 host 自动 Grant。** 预飞已知资源继续自动 Allow；Suggest 等仍会弹 `network_host`。bypass 已显式退出审批，重定向宿主（如 `www.bing.com` → `cn.bing.com`）不应再卡死 headless `exec`。
4. **Engine 自建 Guard 必须带上 `OnNetworkAllow`。** wire 有意不把共享 Guard 传进 thread Engine（审批 handler 隔离），但会话 Gate 的 Grant 回调要经 `Options.OnNetworkAllow` 传下去；否则审批通过后 Dial 仍被拒。
5. **默认 Bing 资源含 `cn.bing.com`。** 国内常把 `www.bing.com` 重定向到该 host；预飞资源列表带上可减少一次中途审批。