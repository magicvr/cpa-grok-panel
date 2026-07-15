# 05. API 契约

## 1. 通用规则

基础路径：`/plugins/cpa-grok-panel/api/v1`。请求/响应使用 UTF-8 JSON，时间为 UTC RFC 3339，ID 为不透明字符串。账号领域 id 即 CPA `auth_index`。修改请求支持 `Idempotency-Key`；响应带 `X-Request-ID`。所有端点由 CPA 管理鉴权保护。

所有会触发 CPA Management status/fields/delete 的账号项必须同时提交 `auth_index` 与 `exact_file_name`。`auth_index` 用于领域定位，`exact_file_name` 用于实际写入；服务端在写前通过最新 list 验证双键映射。host `expected_revision` 是可选字段，仅当 `/meta` 宣告 `conditional_write` 时使用；当前实测无 revision，客户端应省略或提交 `null`，服务端不得伪造 CAS。插件 Settings 的本地 revision 不受此规则影响。

列表默认 `limit=50`，最大 200；分页使用不透明 `cursor`。未知字段的兼容行为：服务端默认拒绝修改请求中的未知字段，读取响应允许客户端忽略新增字段。

## 2. 通用结构

### 2.1 AccountView

```json
{
  "auth_index": "0123456789abcdef",
  "exact_file_name": "account-001.json",
  "enabled": true,
  "priority": 10,
  "host_revision": null,
  "health": {
    "status": "unchecked",
    "checked_at": null,
    "stale": false,
    "latency_ms": null,
    "error_code": null
  },
  "tier": {
    "value": "unknown",
    "checked_at": null,
    "confidence": "unknown"
  },
  "usage": {
    "dedupe_mode": "weak",
    "input_tokens": 100,
    "output_tokens": 40,
    "total_tokens": 140,
    "successful_requests": 1,
    "failed_requests": 0,
    "missing_usage_events": 0,
    "inconsistent_usage_events": 0,
    "period_started_at": "2026-07-15T00:00:00Z",
    "last_usage_at": "2026-07-15T12:34:56Z",
    "complete": true
  },
  "capacity": {
    "tokens": 1000000,
    "source": "default",
    "ratio": 0.00014,
    "exceeded": false
  },
  "failure": {
    "consecutive_attributed_failures": 0,
    "threshold": 3,
    "last_failure_at": null,
    "last_failure_code": null
  },
  "demotion": {
    "state": "none",
    "baseline_priority": null,
    "target_priority": null,
    "triggered_at": null,
    "failure_code": null
  },
  "capabilities": {
    "health_check": false,
    "tier_check": false,
    "responses_test": false,
    "set_enabled": true,
    "restore_priority": false,
    "cleanup": false
  },
  "last_seen_at": "2026-07-15T12:35:00Z"
}
```

### 2.2 OperationView

```json
{
  "id": "op_opaque",
  "type": "health_check",
  "state": "running",
  "requested_at": "2026-07-15T12:00:00Z",
  "started_at": "2026-07-15T12:00:01Z",
  "finished_at": null,
  "progress": {"total": 2, "completed": 1, "succeeded": 1, "failed": 0},
  "results": [
    {"auth_index": "auth_1", "state": "succeeded", "error": null},
    {"auth_index": "auth_2", "state": "running", "error": null}
  ]
}
```

## 3. 系统端点

### `GET /meta`

返回插件版本、API schema、CPA ABI、能力和状态。

```json
{
  "plugin_id": "cpa-grok-panel",
  "plugin_version": "0.1.0",
  "api_version": 1,
  "write_mode": "read_only",
  "status": "ready",
  "state_status": "healthy",
  "statistics_started_at": "2026-07-15T00:00:00Z",
  "dedupe_mode": "weak",
  "conditional_write": false,
  "capabilities": ["usage", "auth_list", "management_routes"],
  "unavailable_features": [
    {"feature": "checks", "reason": "host.auth.invoke unavailable"},
    {"feature": "cleanup", "reason": "M3 feature disabled"}
  ]
}
```

### `GET /me`

返回当前 principal 的显示标识和角色，不返回认证凭据。

## 4. 账号读取

### `GET /accounts`

查询参数：`cursor`、`limit`、`enabled`、`health`、`tier`、`demotion_state`、`search`、`sort`。`search` 只匹配显示字段，不能用于写操作目标解析。

响应：

```json
{
  "items": [],
  "next_cursor": null,
  "snapshot_at": "2026-07-15T12:35:00Z",
  "host_snapshot_revision": null,
  "stale": false
}
```

### `GET /accounts/{auth_index}`

返回单个 AccountView 和最近有限条非敏感审计/操作摘要。不存在返回 404；墓碑默认 404，可通过独立审计端点查询。

## 5. 人工检查

### `POST /operations/health-checks`
### `POST /operations/tier-checks`
### `POST /operations/responses-tests`

通用请求：

```json
{
  "auth_indexes": ["auth_1", "auth_2"]
}
```

Responses 测试可额外携带受限参数：

```json
{
  "auth_indexes": ["auth_1"],
  "model": "host-approved-model-alias",
  "test_profile": "minimal_text",
  "max_output_tokens": 32
}
```

服务端必须忽略/拒绝任意 prompt 字段。成功创建返回 HTTP 202 和 OperationView；目标上限建议 100，Responses 实测建议上限 20。

## 6. 批量启停

### `POST /operations/account-state`

```json
{
  "action": "disable",
  "accounts": [
    {"auth_index": "auth_1", "exact_file_name": "account-001.json"},
    {"auth_index": "auth_2", "exact_file_name": "account-002.json"}
  ],
  "reason": "operator maintenance"
}
```

`action` 为 `enable|disable`。返回 202 OperationView。`reason` 长度受限且审计脱敏。精确文件名不是领域主键，但它是当前 Management 写端不可省略的第二把键；服务端不得仅凭文件名查找账号。

## 7. 恢复优先级

### `POST /operations/restore-priority`

```json
{
  "accounts": [
    {
      "auth_index": "auth_1",
      "exact_file_name": "account-001.json"
    }
  ]
}
```

服务端从持久化 DemotionState 读取 baseline 与该次 `target_priority`，客户端不能提交恢复目标值或写死 `-100`。写前要求当前 priority 等于状态中的 target，写后 re-list 校验。返回 202。

## 8. 删除预检与确认

### `POST /cleanup/preflight`

MVP 单账号请求：

```json
{
  "auth_index": "auth_1",
  "exact_file_name": "account-001.json"
}
```

允许时返回：

```json
{
  "allowed": true,
  "confirmation_token": "short_lived_opaque_token",
  "expires_at": "2026-07-15T12:05:00Z",
  "required_confirmation_text": "DELETE account-001.json",
  "summary": {
    "auth_index": "auth_1",
    "exact_file_name": "account-001.json",
    "enabled": false,
    "priority": -100,
    "demotion_target_priority": -100,
    "last_usage_at": "2026-07-01T00:00:00Z",
    "health": "unhealthy"
  },
  "warnings": []
}
```

拒绝时返回 200 且 `allowed=false`、`blocking_rules[]`，或对权限/输入错误使用对应 4xx。不得为未授权请求泄漏文件存在性。

### `POST /cleanup/confirm`

```json
{
  "auth_index": "auth_1",
  "exact_file_name": "account-001.json",
  "confirmation_token": "short_lived_opaque_token",
  "confirmation_text": "DELETE account-001.json"
}
```

返回 202 cleanup OperationView，或同步成功时 200 明确结果。实现应选择一种并固定；为统一 busy 生命周期，设计推荐 202。确认令牌一次性使用，绑定 principal、双键、写前状态与保护规则摘要；只有宿主实际提供 revision 时才额外绑定 revision。

## 9. 操作查询与取消

### `GET /operations/{operation_id}`

返回 OperationView。只允许有权 principal 查询；审计管理员可看所有，普通 operator 的可见范围待 CPA 角色模型决定。

### `POST /operations/{operation_id}/cancel`

只取消 queued 项和支持取消的 running 检查；已发出的 auth 写或删除不可假定可撤回。返回更新后的 OperationView。取消不是回滚。

## 10. 设置

### `GET /settings`

```json
{
  "revision": 1,
  "operation_concurrency": 3,
  "attributed_failure_threshold": 3,
  "attributed_failure_statuses": [401, 403],
  "demotion_priority": -100,
  "protection_level": "strict",
  "default_token_capacity": 1000000,
  "per_account_token_capacity": {"auth_1": 2000000},
  "health_stale_after_seconds": 86400,
  "operation_timeout_seconds": 60,
  "write_mode": "read_only"
}
```

### `PUT /settings`

提交完整设置对象并增加：

```json
{"expected_revision": 1}
```

成功返回新设置；校验失败返回字段级 `details.fields`；冲突返回 409。不会出现 `auto_check` 或 `auto_delete` 字段。

## 11. 审计（MVP 可只读最近记录）

### `GET /audit`

过滤：`cursor`、`limit`、`auth_index`、`operation_id`、`action`、`since`。响应不包含秘密；文件名是否脱敏由保留策略决定。

## 12. 错误与部分失败

创建批量 operation 成功不代表所有目标成功。HTTP 202 表示已接受，最终状态必须从 operation 获取。创建前的整体校验（未鉴权、请求过大、JSON 非法）使用 4xx 且不创建 operation；目标级不存在、冲突或保护拒绝进入逐项结果。

示例 ItemResult：

```json
{
  "auth_index": "auth_2",
  "state": "failed",
  "before": {"enabled": true, "priority": 10},
  "after": null,
  "error": {
    "code": "write_verification_failed",
    "message": "写入后状态与目标不一致",
    "retryable": false
  }
}
```

## 13. API 安全限制

- `auth_index` 位于 URL 时按不透明段处理并限制长度；禁止把它当路径拼接。
- `exact_file_name` 是所有 Management auth 写请求必需的交叉确认键，但不得单独用于领域查找，也不得拼接磁盘路径；必须以 `.json` 结尾并与最新 list 完全相等。
- 所有数组有限长，字符串有限长，请求体有限制。
- 修改 API 校验 Content-Type、同源/CSRF 和幂等键。
- 响应不返回 auth 文件内容、OAuth token、管理密钥、完整外部 Responses 文本。
