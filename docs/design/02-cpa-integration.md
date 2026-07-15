# 02. CPA 集成契约

## 1. 文档性质

本文件定义插件希望从 CPA 获得的 **目标 ABI 契约**，不是对当前 CPA 实现的事实陈述。实现前必须用 CPA 官方 SDK、文档或维护者确认逐项核对。无法确认的能力必须通过 capability negotiation 禁用，禁止猜测字段或直接操作 CPA 内部文件。

## 2. 契约版本与能力协商

插件声明：

```json
{
  "plugin_id": "cpa-grok-panel",
  "plugin_version": "<semver>",
  "abi_range": ">=1.0 <2.0",
  "required_capabilities": [
    "usage.v1",
    "host.auth.list.v1",
    "management.routes.v1",
    "management.assets.v1"
  ],
  "optional_capabilities": [
    "host.auth.set_enabled.v1",
    "host.auth.set_priority.v1",
    "host.auth.delete.v1",
    "host.auth.invoke.v1",
    "management.principal.v1",
    "conditional_write.v1"
  ]
}
```

注册时 CPA 返回实际 ABI、能力集合、插件专属 data dir、路由挂载上下文和管理认证上下文。缺少 required capability 时注册失败；缺少 optional capability 时对应 UI 动作显示为不可用并解释原因。

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
| `auth_host` | `host.auth.*` 能力句柄 |
| `management_host` | 路由、资源和认证上下文句柄 |

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
  "event_id": "globally-or-host-unique-id",
  "occurred_at": "2026-07-15T12:34:56Z",
  "auth_file_id": "host-stable-auth-id",
  "auth_file_name": "account-001.json",
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

- `event_id`：重放稳定且唯一；没有稳定事件 id 时 MVP 不能承诺精确去重，须与 CPA 协商。
- `auth_file_id`：由 CPA 在实际选定 auth 后填入，是归账与失败降权的唯一可靠身份。
- `auth_file_name`：用于展示和交叉校验，不能替代稳定 id，除非宿主明确只有精确文件名身份。
- `outcome`：至少区分 `success`、`failure`、`cancelled`。取消默认不计入失败阈值。
- `usage`：供应商/CPA 真实报告值；允许整体为 null，字段不得为负数。
- `error`：可含稳定 `code`、HTTP 状态类别、是否可归责于账号；不得含 OAuth token 或完整敏感响应。

`provider` 可作为显示诊断字段，但不得参与 auth 身份映射或降权判断。

### 4.3 token 校验

- 三个字段均存在时，若 `total_tokens != input_tokens + output_tokens`，保留原始三个真实值并标记 `inconsistent`，不自行纠正。
- 仅部分字段存在时分别累计已知值，总量只累计宿主明确提供的 `total_tokens`；UI 显示不完整。
- usage 为 null 时不增加 token，仅更新请求结果与数据质量计数。
- 重复 `event_id` 返回成功确认但不再次累计。

### 4.4 失败归责

只有同时满足以下条件才进入失败阈值：

1. 有可信 `auth_file_id`；
2. `outcome=failure`；
3. CPA 标记 `error.attribution=auth`，或错误码属于经契约确认的 auth 可归责集合；
4. 事件不是管理员取消、客户端断开、全局限流、CPA 内部故障等非账号错误。

若 CPA 不能提供归责字段，初始策略使用保守 allowlist，并把规则版本化。未知错误只计入请求失败统计，不自动降权。

### 4.5 handler 时延与背压

usage handler 不执行外部网络检查。目标是在短时预算内完成校验、去重和状态提交；若宿主允许异步确认，可进入有界持久队列。队列满或 state 不可写时应返回明确可重试错误，不能静默丢弃。

投递语义优先采用 at-least-once + `event_id` 去重。是否支持 CPA 重试、最大时延和乱序事件是实现前阻断问题。

## 5. `host.auth.*` 契约

所有能力以 `auth_file_id` 操作，返回 `revision` 以支持条件写。

### 5.1 `host.auth.list`

返回字段建议：

```json
{
  "items": [
    {
      "auth_file_id": "stable-id",
      "file_name": "account-001.json",
      "auth_type": "oauth",
      "provider": "xai",
      "enabled": true,
      "priority": 10,
      "revision": "opaque-revision",
      "updated_at": "2026-07-15T12:00:00Z",
      "labels": {}
    }
  ],
  "snapshot_revision": "opaque-snapshot"
}
```

插件只纳入经 CPA 正式元数据确认的 xAI OAuth 条目。若 `provider` 只是自由文本，则必须依赖更可靠的 `auth_type`/driver id capability；不得通过读取文件内容自行识别。

### 5.2 `host.auth.get`

按稳定 id 获取最新非秘密元数据，用于删除预检和条件写。接口不得返回 refresh token、access token 或 auth 文件内容。

### 5.3 `host.auth.set_priority`

输入：`auth_file_id`、整数 `priority`、`expected_revision`、`reason`、`idempotency_key`。输出新 revision 和最终值。

冲突时返回 `revision_conflict`，插件重新读取并让操作失败/要求重试，不盲目覆盖外部变更。`-100` 是否为 CPA 正式低优先级语义必须确认；若 CPA 有常量/枚举，以宿主定义为准。

### 5.4 `host.auth.set_enabled`

输入稳定 id、布尔值、条件 revision 和幂等键。停用不等于删除；启用也不自动恢复优先级。

### 5.5 `host.auth.delete`

只允许精确稳定 id + 精确文件名 + revision 条件删除。宿主应再次执行管理鉴权和路径安全校验。若宿主只提供任意文件路径删除，MVP 禁用清理功能。

### 5.6 `host.auth.invoke`（可选）

用于人工健康、套餐或 Responses 检查，使 CPA 在选定 auth 身份下执行受控请求。必须保证指定身份不会被调度替换，并返回实际使用的 `auth_file_id`。若无此能力，插件不得直接读取 auth 文件秘密；相应检查功能禁用，除非 CPA 提供另一正式、安全的 credential lease API。

## 6. Management 路由与资源

### 6.1 路由

插件请求挂载：

- UI：`/plugins/cpa-grok-panel/`
- API：`/plugins/cpa-grok-panel/api/v1/*`

CPA 应处理基础管理鉴权、TLS/反向代理策略和统一前缀。插件仍执行细粒度授权：只读、操作员、管理员。

### 6.2 静态资源

资源由构建产物嵌入或由 CPA 资源接口注册，必须带内容 hash、正确 MIME、`X-Content-Type-Options: nosniff` 和限制性 CSP。禁止从公共 CDN 动态加载脚本。

SPA fallback 只应用于 UI 路由，不吞掉 `/api/v1` 的 404。

## 7. 鉴权与授权

### 7.1 信任边界

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

### 7.2 管理密钥

浏览器可通过 CPA 已有管理认证机制建立会话；插件页面不提供 localStorage 保存密钥的功能。API 不回显密钥，日志对 Authorization、Cookie 和查询参数做脱敏。删除确认不能用管理密钥本身作为确认文本。

### 7.3 Web 安全

- 修改请求要求同源、非 GET 方法及 CPA 提供的 CSRF 防护或严格 bearer 模型。
- CORS 默认关闭；如 CPA 全局开放，插件路由仍限制可信 origin。
- operation 和确认令牌为高熵、短期、绑定 principal 与请求摘要的不可预测值。

## 8. 错误模型

统一错误响应：

```json
{
  "error": {
    "code": "revision_conflict",
    "message": "账号状态已变化，请刷新后重试",
    "request_id": "req_...",
    "retryable": false,
    "details": {
      "field": "expected_revision"
    }
  }
}
```

稳定错误码至少包括：`unauthenticated`、`forbidden`、`invalid_argument`、`capability_unavailable`、`account_not_found`、`revision_conflict`、`protected_account`、`confirmation_expired`、`operation_busy`、`host_unavailable`、`state_unavailable`、`rate_limited`、`internal_error`。

状态码：400 输入错误；401 未认证；403 无权限/保护规则拒绝；404 精确资源不存在；409 revision/operation 冲突；410 确认过期；422 语义不可执行；429 并发/速率限制；502/503 host 或外部检查暂不可用；500 未分类内部错误。

错误信息不得泄漏 auth 是否存在给未授权主体，也不得包含秘密响应正文。

## 9. 版本兼容策略

- ABI 采用区间声明，主版本不匹配直接拒绝加载。
- capability 优先于 CPA 产品版本判断；不以版本字符串猜测能力。
- usage 与 API DTO 均有 `schema_version`，新增可选字段向后兼容；删除/改义需升主版本。
- 对每个支持的 CPA 版本运行契约测试；registry 声明 `cpa_version_range`。
- 遇到未知枚举保留原值供诊断，并映射为 `unknown`，不得崩溃。
- 可选写能力缺失时只读降级；required 读取/usage 能力缺失时拒绝注册。
- 兼容垫片只存在于 `cpaadapter`，不得污染领域或 UI。

## 10. 注册伪流程

```text
验证 host ABI 与 required capabilities
锁定 plugin_data_dir 并加载/迁移 state
构造 auth、repository、check 等适配器
注册 usage handler
注册 management API 与静态资源
同步一次 auth 非秘密元数据
发布 ready；任一步失败则撤销已注册资源并保持 unavailable
```
