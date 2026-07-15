# 03. 领域模型

## 1. 通用约定

- 时间统一保存为 UTC RFC 3339，展示层按浏览器时区格式化。
- token 计数使用非负 64 位整数；溢出时拒绝事件并告警。
- `AuthIndex` 是 CPA 提供的稳定 `auth_index`，作为领域归因主键；不依赖其 16 hex 表现进行业务推断。
- `ExactFileName` 是当前 Management list 返回、以 `.json` 结尾的精确 `name`，只作为写目标键和安全交叉校验，不替代 `AuthIndex`。
- 所有外部枚举允许映射为 `unknown`，同时保留非敏感原始值用于诊断。
- CPA auth 元数据不复制秘密字段到领域或持久化。

## 2. Account

```text
Account
  authIndex: AuthIndex
  exactFileName: ExactFileName
  hostRevision?: OpaqueRevision
  enabled: boolean
  priority: Priority
  authType: oauth | unknown
  providerKind: xai | unknown
  health: HealthObservation
  tier: TierObservation
  usage: UsageCounters
  failure: FailureTracker
  demotion: DemotionState
  protection: ProtectionFacts
  firstSeenAt / lastSeenAt
  tombstonedAt?: timestamp
```

不变量：活跃 Account 必须能在最近一次 Management auth-files list 找到；usage 按 `authIndex` 归因；status/fields/delete 写端必须使用同一次最新映射中的 `exactFileName`；墓碑账号不可执行检查或写操作。

## 3. Health

Health 是一次人工观察，不是永久属性：

```text
HealthObservation
  status: unchecked | healthy | unhealthy | degraded | unknown
  checkedAt?: timestamp
  method?: auth_probe | responses_probe
  latencyMs?: integer
  errorCode?: stable code
  detailSummary?: redacted text
  observedAuthIndex?: AuthIndex
```

- 初始为 `unchecked`。
- 只有检查实际使用的 `auth_index` 与目标一致，结果才可归档到账号。
- 超时可记为 `unknown` 或 `unhealthy` 由检查类型规则决定，MVP 建议 `unknown`，避免把网络故障误判为账号失效。
- UI 根据 `checkedAt` 和设置中的展示过期窗口标记 `stale`；过期不触发自动复查。

## 4. Tier

```text
TierObservation
  value: free | paid | premium | enterprise | unknown
  checkedAt?: timestamp
  source: manual_probe | host_metadata | none
  evidenceCode?: stable non-secret code
  confidence: confirmed | inferred | unknown
```

套餐映射必须由经确认的 xAI 响应字段产生。未知或变化字段映射为 `unknown`，禁止依据 token 用量、优先级、邮箱域名猜套餐。套餐核验默认只由管理员手动触发。

## 5. Priority 与降权

```text
Priority
  current: integer
  source: host

DemotionState
  state: none | queued | applying | applied | failed | superseded | restored
  baselinePriority?: integer
  triggerEventID?: string
  targetPriority?: integer
  triggeredAt?: timestamp
  appliedRevision?: OpaqueRevision
  failureCode?: string
```

规则：

- 降权目标来自 `Settings.demotionPriority`，默认候选 `-100`；实测该整数可写，但仍由配置而非代码魔法数定义。
- 首次自动降权前保存 host 当前 priority 为 `baselinePriority`。
- 已处于插件 `applied` 降权状态且当前 priority 等于该状态保存的 `targetPriority` 时不重复覆盖 baseline。
- 降权后若外部把 priority 改为其他值，则状态标记 `superseded`；插件不自动再次覆盖，除非出现新的阈值周期且策略明确允许。
- “恢复优先级”只恢复已记录 baseline，且要求当前值仍等于该次降权记录的 `targetPriority`。宿主 revision 存在时再附加条件写；不存在时使用串行写和写后 re-list 校验。
- 没有 baseline 时不得猜默认优先级；UI 提示不可恢复，可由管理员使用 CPA 原生能力设置。
- 恢复是人工动作，不自动发生。
- usage handler 只把状态推进到 `queued` 并入队；Management PATCH 由异步 worker 执行。

## 6. UsageCounters

```text
UsageCounters
  inputTokens: uint64
  outputTokens: uint64
  totalTokens: uint64
  successfulRequests: uint64
  failedRequests: uint64
  cancelledRequests: uint64
  eventsWithMissingUsage: uint64
  eventsWithInconsistentUsage: uint64
  unattributedEvents: uint64 (global only)
  periodStartedAt: timestamp
  lastUsageAt?: timestamp
  lastEventID?: string (diagnostic only)
  dedupeMode: exact | weak
```

累计规则：

- 只累加事件中明确提供的真实 token 字段。
- 计数是插件统计期内累计，不自动按供应商账期清零。
- `totalTokens` 不从输入+输出推导，除非 CPA 正式保证总量字段永远等式且领域契约相应修订。
- 每账号上限 `tokenCapacity` 仅用于计算 `usageRatio = knownTotal / capacity`；capacity 为空或 0 时比例未知。
- 超过上限只显示超量/告警，不阻断 CPA 调度，不触发删除或降权。
- 重置计数不属于 MVP；如未来加入，必须保留审计和历史周期。
- `dedupeMode=exact` 仅在宿主提供经证明稳定的 `event_id` 时成立；`weak` 使用短窗合成指纹，可能漏重或误判，不得作为账本。

## 7. FailureTracker

```text
FailureTracker
  consecutiveAttributedFailures: integer
  firstFailureAt?: timestamp
  lastFailureAt?: timestamp
  lastFailureCode?: string
  policyVersion: integer
  attributedStatusAllowlist: set<http status>
```

MVP 采用连续失败阈值：具有可信 `auth_index` 且命中版本化 allowlist 的失败 +1；可信成功归零；取消、未知归责和缺少身份不改变连续值。默认 allowlist 为 401/403，429/5xx 是否纳入由配置决定；设置变化不追溯重算已有事件。

当计数从 `threshold-1` 到 `threshold` 时只产生一次降权意图。执行失败不通过伪造新的 usage 事件重试；由操作状态或管理员显式重试控制。

## 8. Settings

```text
Settings
  schemaVersion: integer
  operationConcurrency: integer
  attributedFailureThreshold: integer
  attributedFailureStatuses: set<http status>
  demotionPriority: integer
  protectionLevel: standard | strict | maximum
  defaultTokenCapacity?: uint64
  perAccountTokenCapacity: map<AuthIndex, uint64>
  healthStaleAfterSeconds: integer
  operationTimeoutSeconds: integer
  writeMode: read_only | managed
  revision: integer
```

建议初值：并发 3、失败阈值 3、失败状态 401/403、`demotionPriority=-100`、保护档位 `strict`、检查过期 24 小时、单操作超时 60 秒、共存灰度初始 `read_only`。确切范围在性能测试后冻结。

不变量：

- 并发范围建议 1–20；阈值范围 1–100。
- token capacity 必须为正整数或缺省。
- 插件自身 Settings 更新必须携带本地 `expected_revision` 并整体原子生效；这与 CPA auth 写端当前缺少 host revision 无关。
- 设置不能开启自动检查或自动删除，因为领域中不存在这些开关。

## 9. 保护规则

ProtectionFacts 是从 host 元数据、插件状态和当前操作计算出的事实。保护档位决定删除是否允许：

| 规则 | standard | strict | maximum |
| --- | --- | --- | --- |
| 必须为 xAI OAuth | 必须 | 必须 | 必须 |
| `auth_index` 与精确 `.json` name 匹配 | 必须 | 必须 | 必须 |
| 账号已停用 | 建议，警告 | 必须 | 必须 |
| 存在 applied demotion 且当前 priority=该记录 target | 否 | 必须 | 必须 |
| 最近有 usage（默认 24h） | 警告 | 拒绝 | 拒绝 |
| 唯一剩余可用账号 | 警告 | 拒绝 | 拒绝 |
| 有运行中 operation | 拒绝 | 拒绝 | 拒绝 |
| 健康状态非 unhealthy | 警告 | 警告 | 拒绝 |
| 人工健康结果过期/未检查 | 允许 | 警告 | 拒绝 |
| 确认令牌 + 确认文本 | 必须 | 必须 | 必须 |

无论档位如何，管理鉴权、精确身份、路径安全和运行中操作规则不可关闭。保护规则只增加安全门槛，不能降低 host 自身限制。

## 10. Operation

```text
Operation
  id: OperationID
  type: health_check | tier_check | responses_test | enable | disable | restore_priority | cleanup
  requestedBy: PrincipalRef
  requestedAt / startedAt? / finishedAt?
  state: queued | running | succeeded | partially_succeeded | failed | cancelled
  targets[]: AuthIndex
  results[]: ItemResult
  expiresAt?: timestamp
```

`ItemResult` 状态为 queued/running/succeeded/failed/skipped/cancelled，并带稳定错误码。操作达到终态后不可回到运行态。API 幂等键相同且请求摘要相同应返回原 operation；相同键不同请求返回冲突。

## 11. AuditEvent

记录时间、principal 引用、动作、目标 id 的不可逆摘要、请求 id、before/after 非秘密字段、结果和错误码。不得记录管理密钥、OAuth token、完整文件内容、完整外部响应。墓碑保留原 id 的哈希、精确文件名（按保留政策决定是否脱敏）、删除时间和审计关联。
