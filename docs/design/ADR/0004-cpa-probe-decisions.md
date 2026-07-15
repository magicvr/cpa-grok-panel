# ADR 0004：按 CPA 实测冻结能力决策

- 状态：已接受
- 日期：2026-07-15
- 依据：`docs/reviews/2026-07-15-cpa-capability-probe-report.md`

## 背景

初版设计把稳定单一 auth id、全局 `event_id`、revision 条件写和锁定 auth invoke 作为目标契约。2026-07-15 的 Management REST 与已加载插件 usage 链路实测已经足以冻结 M1–M3 的边界，避免实现阶段把未证实能力当成事实。

## 冻结的五问

| 五问 | 状态 | 决策 |
| --- | --- | --- |
| Q1 身份与 event_id | partial | `auth_index` 是领域归因主键；当前 list 的精确 `.json` `name` 是所有 Management 写端口必需键。两者不可互换。稳定 `event_id` 有则精确去重，无则 `weak_dedupe` 并明确非账本级。 |
| Q2 失败归责 | resolved | 不依赖未出现的 attribution 枚举。采用版本化 HTTP 状态 allowlist：401/403 默认计入；429/5xx 可配置；未知、取消和非账号错误不触发降权。 |
| Q3 priority 与 revision | resolved | `PATCH /auth-files/fields` 以精确 name 可写整数 priority。`demotion_priority` 可配置，默认候选 `-100`。列表无 revision，因此使用按账号串行、写前/写后 re-list、防抖与 LWW；禁止声称 CAS，自动降权不得依赖 revision。 |
| Q4 锁定 auth 检查 | partial | Management REST 未发现 invoke/lease。`host.auth.invoke` 或等价安全路径保持 optional；缺失时健康、套餐和 Responses 整组 unavailable 或后置，不读取 auth 文件秘密补能力。 |
| Q5 MVP 砍法 | resolved | M1=list+usage+state+只读 UI，零 auth 写；M2=status 启停+fields 异步降权/恢复；M3=有路径时的检查+双确认安全删除。删除可以不进入 MVP。 |

## 具体后果

- Management REST 与插件 host 分开建模：计量只依赖 `usage.handle`，不依赖实测 404 的 `/v0/management/usage`。
- usage handler 只做校验、记账和降权意图入队；Management PATCH 在异步 worker 中执行。
- DemotionState 保存 `targetPriority`；保护和恢复判断使用“存在 applied demotion 且当前 priority 等于该 target”，不写死 `-100`。
- 所有 status、fields、delete 请求都必须先验证 `auth_index`→`exact_file_name` 当前映射，再以精确 name 调用 Management REST。
- `expected_revision` 保留为未来 optional enhancement；当前 API 中可省略或为 null。
- 与旧 `grok-panel` 可通过不同 plugin id 并存，但同一时刻只能有一个 auth 写责任方。

## 被拒绝方案

- 把文件名作为领域主键：无法满足 usage 以稳定 index 归因。
- 仅用 `auth_index` 调用写接口：实测 fields PATCH 会返回 `name is required`。
- 在无 revision 时模拟 CAS：会制造不存在的安全保证。
- 在 usage handler 内同步 PATCH：会把 Management 延迟和失败带入热路径。
- 因无 invoke 而读取 auth 文件或秘密直连：越过宿主安全边界。
- 把自动降权作为 M1/第一可用门槛：与只读、低风险灰度目标冲突。
