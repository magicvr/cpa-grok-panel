# 02. CPA 集成契约

## 1. 文档性质

本文件同时记录 2026-07-15 已实测的 CPA 契约与尚待确认的插件 host 目标能力。Management REST 与插件 host 是两个边界：前者已证实 list/status/fields/delete，后者已证实 `usage.handle` 链路可工作；`host.auth.invoke`、全局稳定 `event_id` 与条件写仍未证实。禁止把目标能力写成现状，也禁止直接操作 CPA 内部文件。

## 2. 契约版本与能力协商

插件声明：

```json
{
  "plugin_id": "cpa-grok-panel",
  "plugin_version": "<semver>",
  "abi_range": ">=1.0 <2.0",
  "required_capabilities": [
    "usage.v1",
    "management.auth_files.v1",
    "management.routes.v1",
    "management.assets.v1"
  ],
  "optional_capabilities": [
    "host.auth.invoke.v1",
    "management.principal.v1",
    "conditional_write.v1"
  ]
}
```

注册时 CPA 返回实际 ABI、能力集合、插件专属 data dir、路由挂载上下文和管理认证上下文。Management REST 能力可由受控启动探测或版本化适配器确认，不得只靠产品版本猜测。缺少 required capability 时注册失败；缺少 optional capability 时对应 UI 动作显示为不可用并解释原因。

## 3. 生命周期契约

### 3.1 `register`

目标语义：CPA 加载插件后调用一次，用于声明 handler、management routes 和 assets。

输入概念字段：

| 字段 | 含义 |
| --- | --- |
| `host_abi_version` | CPA 插件 ABI 版本 |
| `host_version` | CPA 产品版本，仅作诊断 |
| `capabilities[]` | 宿主支持的正式能力 |
| `plugin_data_dir` | CPA 分配的专属可写目录 |
| `logger` / `metrics` | 宿主可选可观测接口 |
| `auth_host` | 可选 `host.auth.invoke` 等插件 host 能力句柄 |
| `management_host` | 路由、资源、认证上下文及受控 Management REST 访问句柄 |

输出概念字段：注册成功、已注册能力、降级功能列表或结构化失败。`register` 必须是幂等初始化；同一实例重复调用应拒绝或返回同一结果，不重复注册路由。

失败场景：ABI 不兼容、required capability 缺失、data dir 不安全/不可写、state 迁移失败、实例锁占用、路由冲突。任何失败都必须阻止半初始化插件提供写操作。

### 3.2 `reconfigure`

CPA 在配置变化时传入新配置和 revision。插件先完整校验，再原子替换内存设置并持久化。失败时返回字段级错误并继续使用旧配置，不允许部分生效。

敏感配置（若未来存在检查代理凭据）应由 CPA secret reference 提供，API 和 state 只保存引用，不保存秘密值。

### 3.3 shutdown（待 CPA 确认）

若 ABI 提供 shutdown hook，插件停止接单、取消未开始任务、给执行中任务有限排空时间、刷新 state 并释放锁。若无 hook，原子 state 写和幂等 operation 必须保证进程中止后可恢复。

## 4. `usage.handle` 契约

### 4.1 最小事件

```json
{
  "schema_version": 1,
  "event_id": "optional-host-event-id",
  "occurred_at": "2026-07-15T12:34:56Z",
  "auth_index": "16-hex-stable-index",
  "name": "account-001.json",
  "request_id": "host-request-id",
  "model": "grok-model-name",
  "outcome": "success",
  "usage": {
    "input_tokens": 100,
    "output_tokens": 40,
    "total_tokens": 140
  },
  "error": null
}
```

### 4.2 必需语义

- `event_id`：可选。宿主提供且证明重放稳定时用于精确去重；当前未证明时进入 `weak_dedupe`，不得承诺账本级精确性。
- `auth_index`：由 CPA 在实际选定 auth 后提供或可可靠提取，是归账与失败降权的领域主键。
- `name`：用于展示和与 Management 清单交叉校验；写端必须使用当前 list 返回的精确 `.json` 名称。
- `outcome`：至少区分 `success`、`failure`、`cancelled`。取消默认不计入失败阈值。
- `usage`：供应商/CPA 真实报告值；允许整体为 null，字段不得为负数。
- `error`：可含稳定 `code`、HTTP 状态类别、是否可归责于账号；不得含 OAuth token 或完整敏感响应。

`provider` 可作为显示诊断字段，但不得参与 auth 身份映射或降权判断。

### 4.3 token 校验

- 三个字段均存在时，若 `total_tokens != input_tokens + output_tokens`，保留原始三个真实值并标记 `inconsistent`，不自行纠正。
- 仅部分字段存在时分别累计已知值，总量只累计宿主明确提供的 `total_tokens`；UI 显示不完整。
- usage 为 null 时不增加 token，仅更新请求结果与数据质量计数。
- 精确模式下重复 `event_id` 返回成功确认但不再次累计；弱去重命中时记录命中原因和模式。

### 4.4 失败归责

只有同时满足以下条件才进入失败阈值：

1. 有可信 `auth_index`；
2. `outcome=failure`；
3. 错误码属于版本化 allowlist；当前默认 401/403，429/5xx 是否纳入由配置决定；
4. 事件不是管理员取消、客户端断开、全局限流、CPA 内部故障等非账号错误。

当前未发现正式 `attribution` 枚举，因此不以它为依赖，也不虚构该字段。未知错误只计入请求失败统计，不自动降权。

### 4.5 handler 时延与背压

usage handler 不执行 Management PATCH 或外部网络检查。目标是在短时预算内完成校验、记账和降权意图入队；降权由独立有界异步队列执行。队列满或 state 不可写时应记录明确失败并按宿主契约返回可重试错误，不能静默丢弃。

投递语义以实际插件回调为准。`event_id` 存在时采用精确去重；否则采用文档化的短窗合成指纹弱去重，并暴露 `dedupe_mode=weak` 与重复风险。

## 5. Management auth-files REST（已实测）

所有写操作采用双键校验：领域/API 目标含 `auth_index`，适配器写前从最新 list 验证其当前 `exact_file_name`，实际 Management 请求只发送精确 `name`。当前响应无 revision，不能伪造条件写。

### 5.1 列表：`GET /v0/management/auth-files`

目标投影字段：

```json
{
  "items": [
    {
      "auth_index": "0123456789abcdef",
      "name": "account-001.json",
      "id": "account-001.json",
      "type": "xai",
      "provider": "xai",
      "disabled": false,
      "status": "ready",
      "priority": 10,
      "status_message": ""
    }
  ]
}
```

列表还可包含 `path`、`account_type`、`unavailable`、`success`、`failed` 与 `recent_requests` 等字段。插件只纳入元数据确认的 xAI OAuth 条目；不得通过读取文件内容自行识别。列表没有 `revision` 或 snapshot revision。

### 5.2 启停：`PATCH /v0/management/auth-files/status`

请求体：`{"name":"<exact.json>","disabled":true|false}`。`name` 必须来自最新 list 且以 `.json` 结尾；仅 `auth_index` 不足以写入。写后必须 re-list 验证目标映射和最终状态。

### 5.3 priority：`PATCH /v0/management/auth-files/fields`

请求体：`{"name":"<exact.json>","priority":-100}`。`priority` 为整数，`demotion_priority` 可配置，默认候选 `-100`。实测仅 `auth_index` 或 `names[]` 会返回 `name is required`。

无 revision 时采用：按 `auth_index` 串行化、写前 re-list、防抖、发送精确 name、写后 re-list 校验，并接受 last-write-wins。保护和恢复规则依据 DemotionState 与 `current_priority == target_priority`，不把 `-100` 写死为唯一语义。

### 5.4 删除：`DELETE /v0/management/auth-files`

路由存在，按 body 或 query 中精确 `name` 删除；未对真实账号执行删除探测。功能仅列入 M3，必须双确认、再次核对 `auth_index`→`name`、保护规则和写后 re-list。MVP 可以不实现。

### 5.5 条件写（可选、当前不支持）

若未来 CPA 返回 revision/ETag，适配器可以启用 `conditional_write` 并接受可选 `expected_revision`。当前不得发送虚构 revision、声称 CAS，或因没有 revision 而禁用已证实可用的 M2 status/fields 写入。

## 6. 插件 host 能力

### 6.1 `usage.handle`（已证实链路可用）

已加载插件的 usage debug 证明回调链路能够收到失败、状态码、provider 和降权结果。计量以该回调为准；`GET /v0/management/usage` 实测 404，不属于依赖。

### 6.2 `host.auth.invoke`（可选、未证实）

用于人工健康、套餐或 Responses 检查，使 CPA 在选定 auth 身份下执行受控请求。必须保证指定身份不会被调度替换，并返回实际使用的 `auth_index`。Management REST 未发现该能力；若插件 host 也不提供，健康/套餐/Responses 整组禁用或后置，插件不得直接读取 auth 文件秘密。

## 7. 插件 Management 路由与资源

### 7.1 路由

插件请求挂载：

- UI：`/plugins/cpa-grok-panel/`
- API：`/plugins/cpa-grok-panel/api/v1/*`

CPA 应处理基础管理鉴权、TLS/反向代理策略和统一前缀。插件仍执行细粒度授权：只读、操作员、管理员。

### 7.2 静态资源

资源由构建产物嵌入或由 CPA 资源接口注册，必须带内容 hash、正确 MIME、`X-Content-Type-Options: nosniff` 和限制性 CSP。禁止从公共 CDN 动态加载脚本。

SPA fallback 只应用于 UI 路由，不吞掉 `/api/v1` 的 404。

## 8. 鉴权与授权

### 8.1 信任边界

- 外层：CPA 验证管理密钥/会话并生成不可伪造 principal。
- 内层：插件根据 principal role 授权动作。
- 插件不接受浏览器自报的角色、用户名或“已认证”标志。

建议角色：

| 角色 | 权限 |
| --- | --- |
| `viewer` | 列表、详情、usage、操作结果读取 |
| `operator` | viewer + 人工检查、批量启停、恢复优先级 |
| `admin` | operator + 设置、删除预检与确认 |

若 CPA 只支持单一管理密钥，则所有成功鉴权请求映射为 `admin`，但仍应在审计中标识认证方式。

### 8.2 管理密钥

浏览器可通过 CPA 已有管理认证机制建立会话；插件页面不提供 localStorage 保存密钥的功能。API 不回显密钥，日志对 Authorization、Cookie 和查询参数做脱敏。删除确认不能用管理密钥本身作为确认文本。

### 8.3 Web 安全

- 修改请求要求同源、非 GET 方法及 CPA 提供的 CSRF 防护或严格 bearer 模型。
- CORS 默认关闭；如 CPA 全局开放，插件路由仍限制可信 origin。
- operation 和确认令牌为高熵、短期、绑定 principal 与请求摘要的不可预测值。

## 9. 错误模型

统一错误响应：

```json
{
  "error": {
    "code": "write_verification_failed",
    "message": "写入后状态与目标不一致，请刷新核对",
    "request_id": "req_...",
    "retryable": false,
    "details": {
      "field": "exact_file_name"
    }
  }
}
```

稳定错误码至少包括：`unauthenticated`、`forbidden`、`invalid_argument`、`capability_unavailable`、`account_not_found`、`identity_mapping_changed`、`write_verification_failed`、`revision_conflict`（仅条件写可用时）、`protected_account`、`confirmation_expired`、`operation_busy`、`host_unavailable`、`state_unavailable`、`rate_limited`、`internal_error`。

状态码：400 输入错误；401 未认证；403 无权限/保护规则拒绝；404 精确资源不存在；409 身份映射、可选 revision 或 operation 冲突；410 确认过期；422 语义不可执行；429 并发/速率限制；502/503 host 或外部检查暂不可用；500 未分类内部错误。

错误信息不得泄漏 auth 是否存在给未授权主体，也不得包含秘密响应正文。

## 10. 版本兼容策略

- ABI 采用区间声明，主版本不匹配直接拒绝加载。
- capability 优先于 CPA 产品版本判断；不以版本字符串猜测能力。
- usage 与 API DTO 均有 `schema_version`，新增可选字段向后兼容；删除/改义需升主版本。
- 对每个支持的 CPA 版本运行契约测试；registry 声明 `cpa_version_range`。
- 遇到未知枚举保留原值供诊断，并映射为 `unknown`，不得崩溃。
- 可选写能力缺失时只读降级；required 读取/usage 能力缺失时拒绝注册。
- 兼容垫片只存在于 `cpaadapter`，不得污染领域或 UI。

## 11. 注册伪流程

```text
验证 host ABI 与 required capabilities
锁定 plugin_data_dir 并加载/迁移 state
构造 Management auth、repository、check 等适配器
注册 usage handler
注册 management API 与静态资源
同步一次 auth 非秘密元数据
发布 ready；任一步失败则撤销已注册资源并保持 unavailable
```
