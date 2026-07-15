# 09. 风险与开放问题

## 1. 主要风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| CPA ABI 与目标契约不一致 | 功能无法落地 | capability 协商；缺则降级；实现前实测 |
| usage 无稳定 event_id | 重复累计 | `weak_dedupe` + 模式明示；不作为账本 |
| usage 无可信 auth 身份 | 无法降权 | 不猜测；指标暴露未归因事件 |
| 失败归责不准 | 误降权 | 保守 allowlist；人工可恢复 baseline |
| 与旧 grok-panel 双写 | 优先级互相覆盖 | 单一写责任；write_mode |
| host 无条件写 | 覆盖竞态 | 单账号串行、写前/写后 re-list、防抖、接受 LWW；禁止伪装 CAS |
| 直接操作 auth 文件诱惑 | 安全/兼容灾难 | 架构禁止；评审拦截 |
| token 上限被误解为账单 | 错误运营决策 | UI 文案强调“运维分母” |
| 检查产生真实费用 | 成本 | 严格限流、最小 profile、确认文案 |
| state 损坏 | 丢失统计 | bak + 诊断模式；auth 真相仍在 CPA |

## 2. 五问冻结（2026-07-15 实测）

详细决策见 `ADR/0004-cpa-probe-decisions.md`。

| 五问 | 状态 | 冻结答案 |
| --- | --- | --- |
| Q1 身份与 event_id | partial | 身份已解决为双键：`auth_index` 归因、精确 `.json` `name` 写入；全局稳定 `event_id` 未证明，有则 exact，无则 `weak_dedupe` |
| Q2 失败归责 | resolved | 无正式 attribution 枚举；采用版本化 allowlist，默认 401/403，429/5xx 可配置，未知错误不降权 |
| Q3 priority 与 revision | resolved | fields PATCH 可写整数 priority；`demotion_priority` 可配置，默认候选 `-100`；revision 实测不支持，使用串行/re-list/防抖/LWW，禁止假装 CAS |
| Q4 锁定 auth 检查 | partial | Management REST 无 invoke/lease 证据；`host.auth.invoke` 保持 optional，无安全锁定路径则健康/套餐/Responses 整组不可用或后置 |
| Q5 MVP 砍法 | resolved | M1 只读 list+usage+state+UI、零 auth 写；M2 status+fields；M3 按能力检查+双确认删除，删除可不进 MVP |

## 3. 原开放问题处置

| 原问题 | 处置 |
| --- | --- |
| 稳定身份、event_id、失败归责、priority、条件写、invoke、删除、只读首发 | 已由五问冻结为 resolved/partial，不再阻断 M1 |
| 管理角色模型 | open：等待 CPA principal/管理认证契约 |
| 灰度写责任切换 | resolved：接受人工 runbook，任何时刻只允许一个写责任方 |
| usage 统计起点 | resolved：从新插件启用日起算，不迁移旧插件 state |
| Responses 是否进入降权 | resolved：只由正常 `usage.handle` 与同一版本化归责规则决定，检查服务不重复记账 |
| 前端技术选型 | open：不影响 M1 契约冻结 |
| state debounce 丢盘窗口 | open：需压测后定秒级预算，不改变 `weak_dedupe` 精度声明 |
| UI 并存命名 | open：M4 灰度前冻结 |

## 4. 当前非阻断开放问题

1. CPA principal 是单一管理密钥还是可区分 viewer/operator/admin？
2. raw usage 在未来版本是否提供稳定 `event_id`，可否从 weak 升 exact？
3. 插件 host 是否提供 `host.auth.invoke` 或等价 credential lease？
4. 前端采用轻量原生还是小型框架？
5. usage debounce 的可接受丢盘窗口是多少？
6. 新旧面板菜单命名如何降低误操作？

## 5. 明确不做的“捷径”

- 不读旧插件源码“对齐行为”
- 不解析 auth JSON 秘密字段做套餐猜测
- 不在浏览器存管理密钥
- 不自动巡检、不自动删除
