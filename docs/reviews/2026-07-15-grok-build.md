# 设计风险审查

范围：仅 `docs/design/**` 与根 `README.md`（目标契约/设计基线，非 CPA 实现事实）。整体方向（clean-room、host API 写路径、真实 usage、人工检查、能力协商）是对的；主要风险集中在**未冻结的宿主契约被当成领域不变量**、**usage 热路径与写副作用耦合**，以及**文档间对 MVP/降权语义的不一致**。

---

## Critical

### C1. 身份与 usage 去重契约未冻结，却支撑整条成功标准

| 项 | 内容 |
| --- | --- |
| **问题** | 归账、失败降权、删除交叉确认、Account 主键均依赖稳定 `auth_file_id`；精确计量依赖稳定且可重放的 `event_id` + at-least-once。二者在文档中明确是**目标契约/阻断开放问题**，但成功标准与 M1 验收已按“已具备”书写。 |
| **位置** | `README.md`（定位/成功前提）；`00-overview.md` §4.1、§5；`02-cpa-integration.md` §4.1–4.5；`09-risks-and-open-questions.md` §2.1–2.2；`10-mvp-plan.md` M1 验收 |
| **为何高风险** | 若宿主只有易变文件名、或 usage 无稳定 id / 语义为 best-effort：会出现**静默错账、重放双计、不可安全降权**。此时继续做“精确累计 + 自动降权”不是功能缺口，而是**错误产品承诺**。 |
| **建议修正** | M0 产出**实测契约矩阵**（有/无 `auth_file_id`、id 生命周期、rename、`event_id` 范围与重试）。无稳定 id → M1 只做“按精确文件名投影 + 未归因指标”，**禁止**自动降权；无稳定 `event_id` → 文档与 UI 改为“弱一致计量”，验收去掉“重放不双计”，或阻断发布直到宿主补齐。 |

### C2. usage 处理路径内联 `set_priority`，且与 state 提交无跨系统事务

| 项 | 内容 |
| --- | --- |
| **问题** | 架构把“累计 → 达阈值 → `host.auth.set_priority(..., -100)` → 持久化”串在同一 usage 链路；host 写成功但 state 失败（或反过来）会导致**host 已降权但 baseline/DemotionState 丢失**，或**state 认为已 applied 而 host 未变**。 |
| **位置** | `01-architecture.md` §4.1 步骤 5–7；`diagrams/data-flow.md`（usage 与降权）；`03-domain-model.md` §5 DemotionState；`02-cpa-integration.md` §4.5（handler 短时预算） |
| **为何高风险** | 这是典型双写分裂：恢复优先级依赖 baseline；保护规则与 UI 依赖 demotion state。任一方向不一致都会造成**不可恢复、误恢复或重复降权**。usage 热路径同步 host 写还会拖垮 at-least-once 确认，引发重试风暴或丢事件。 |
| **建议修正** | 领域只产出 `DemotionRequested`；**异步有界 demotion worker** 执行 host 写。状态机强制：`requested → applied|failed`，host 调用前先原子落库意图 + baseline + `idempotency_key`；applied 必须以 host 返回的新 `revision`/priority 为准再提交。usage handler **永不**同步调用会改变调度的 host 写。 |

### C3. `-100` 与“已降权”被写进保护规则与恢复不变量，但语义未证实

| 项 | 内容 |
| --- | --- |
| **问题** | 降权目标、恢复前置条件（当前值仍为插件降权值）、strict/maximum 删除门槛（priority 已为 `-100`）全部硬编码 `-100`。文档已承认“是否为 CPA 正式低优先级语义必须确认”。 |
| **位置** | `00-overview.md` §2；`03-domain-model.md` §5、§9 保护表；`04-features.md` §3/§9/§10；`05-api-contracts.md` restore 的 `expected_current_priority: -100` |
| **为何高风险** | 若 CPA 优先级方向相反、范围不含负数、或 `-100` 无特殊含义：自动降权**无效或反向伤害调度**；更严重的是 strict 删除规则变成**永久锁死清理**或**形同虚设**。恢复 API 也会误判 `priority_superseded`。 |
| **建议修正** | 引入配置/常量 `demotion_priority`（或宿主枚举 `priority_class=deprioritized`），默认值仅作候选；保护规则改为“`demotion.state==applied` 且 host 当前 priority **等于记录的 demotionTarget**”，禁止魔法数散落。M2 启动条件：契约测试证明 set_priority 对该值的调度效果。 |

### C4. 自动降权被列入“第一可用版本”，但归责与写能力仍是 optional/未知

| 项 | 内容 |
| --- | --- |
| **问题** | `required_capabilities` 不含 `set_priority`/归责字段；失败归责依赖 `error.attribution` 或 allowlist（开放问题）；同时 `10-mvp-plan` 成功定义要求“失败账号被自动降到 -100 且可手动恢复”。 |
| **位置** | `02-cpa-integration.md` §2、§4.4；`10-mvp-plan.md` §1、§5；`09-risks-and-open-questions.md` §2.3–2.5 |
| **为何高风险** | 在归责不可靠时上线自动降权 = **误伤生产调度**（可用账号被挤出）。把 optional 能力写成 MVP 成功标准会造成实现团队“用猜测填洞”的压力，直接违反文档自己的 fail-closed 原则。 |
| **建议修正** | 成功定义拆层：**M1 可用 = 只读+usage+数据质量**；**M2 可用 = 契约证实后的受控写+降权**。未证实归责前，`write_mode=managed` 也**默认关闭自动降权**（仅统计），需显式 `demotion_enabled` + 规则版本。 |

---

## High

### H1. 无 `host.auth.invoke`（或等价 lease）时，检查/套餐/Responses 设计过约束又欠约束

| 项 | 内容 |
| --- | --- |
| **问题** | 检查正确性依赖“指定 auth 且不被调度替换，并回传实际 `auth_file_id`”。能力为 optional；缺失时禁用。但产品总览仍把人工健康/套餐/Responses 列为目标能力，且未定义**除 invoke 外的唯一合法替代**的冻结决策。 |
| **位置** | `02-cpa-integration.md` §5.6；`01-architecture.md` §4.3；`04-features.md` §4–6；`03-domain-model.md` §3 `observedAuthFileID` |
| **为何高风险** | 若实现“走普通代理请求”却无法证明实际 auth：健康/套餐会**张冠李戴**；Responses 费用与失败还可能通过 usage 触发降权（见 H3）。 |
| **建议修正** | 冻结三态：`locked_invoke` / `unavailable` /（仅当宿主提供）`credential_lease`。无前两者 → M3 功能在 capability 层整组关闭，API 返回 `capability_unavailable`，UI 不可点。禁止“尽力调度”灰度。 |

### H2. 条件写（revision）缺失时，自动降权 + 批量启停 + 共存旧插件不可安全组合

| 项 | 内容 |
| --- | --- |
| **问题** | 设计正确强调 `expected_revision`；缺失时原则是降级危险写。但自动降权、批量 enable/disable、与旧插件灰度写责任切换会在 **LWW** 下互相覆盖，`write_mode` 又只是本插件本地开关，**不能约束旧插件**。 |
| **位置** | `02-cpa-integration.md` §5.3–5.5；`01-architecture.md` §5；`ADR/0003`；`09-risks-and-open-questions.md` §1 双写行 |
| **为何高风险** | 生产最常见事故不是“删错”，而是**优先级被两个写者来回打**，表现为随机路由与“恢复无效”。 |
| **建议修正** | 无 `conditional_write.v1`：禁止自动降权；人工写操作改为“读取-展示-确认-短窗口再写”并在 UI 强提示竞态；灰度 **硬性 runbook 门禁**（checklist + 双人确认）， ideally 宿主侧互斥。有条件写：所有 host 写强制带 revision + idempotency。 |

### H3. Responses/健康探测失败经 usage 进入降权，运维自测可打残账号池

| 项 | 内容 |
| --- | --- |
| **问题** | 规格写明测试 usage 走正常回调，降权完全交给 CPA usage 归责；开放问题 11 未决。 |
| **位置** | `04-features.md` §6；`09-risks-and-open-questions.md` §2.11 |
| **为何高风险** | 批量健康/Responses 在账号异常或限流时，可在短时间打满连续失败阈值，**自测导致自动降权**。 |
| **建议修正** | 默认：带插件 `operation_id`/诊断标记的事件 **不计入** `consecutiveAttributedFailures`（若 usage 无关联字段，则检查期间对该 `auth_file_id` 进入 `demotion_suppress` 租约）。至少提供设置 `count_probe_failures_toward_demotion=false`（默认 false）。 |

### H4. 去重结构允许 bloom，假阳性会少计 token

| 项 | 内容 |
| --- | --- |
| **问题** | `event_dedupe` 概念为 “ring-or-bloom”；bloom 假阳性会把新事件当重放，**少计**且难审计。 |
| **位置** | `07-persistence.md` §3、§5；`02-cpa-integration.md` §4.3 |
| **为何高风险** | 与“真实计量、不估算”原则直接冲突；运营会用错误总量做容量决策。 |
| **建议修正** | MVP **禁止** bloom 作唯一去重；用有界 LRU/环形精确集合 + 可选二级磁盘。窗口满时策略写死：宁可通过指标暴露 `dedupe_window_evictions`，并与宿主协商可接受窗口；文档删除 bloom 或标为“仅允许 zero-FP 结构”。 |

### H5. debounce 落盘与“不可静默丢弃”自相矛盾

| 项 | 内容 |
| --- | --- |
| **问题** | usage 在 state 不可写时应可重试；同时又允许短 debounce 合并写，并开放“可接受丢盘窗口”。崩溃窗口内若 handler 已向 CPA 返回成功，则**事件永久丢失**。 |
| **位置** | `07-persistence.md` §4、§9；`02-cpa-integration.md` §4.5 |
| **为何高风险** | at-least-once 的正确性建立在“成功确认 ⇔ 已提交去重+计数”。提前 ACK = 放弃精确计量。 |
| **建议修正** | 冻结二选一：**同步落盘后再 ACK**（推荐 M1），或 **WAL/append-only 先持久化再 ACK**，内存 debounce 只作用于快照压缩。禁止“ACK 后仍可能丢”的 debounce。 |

### H6. xAI OAuth 筛选依赖不可靠 `provider`，可靠替代未进 required

| 项 | 内容 |
| --- | --- |
| **问题** | 正确禁止用 provider 做身份/降权，但列表“只纳入 xAI OAuth”仍可能落到 free-text provider；可靠 `auth_type`/driver capability 未列入 required。 |
| **位置** | `02-cpa-integration.md` §5.1；`04-features.md` §1；`00-overview.md` §4.1 |
| **为何高风险** | 漏纳 → 面板空/不全；误纳 → 对错误 auth 做启停/降权/删除预检。 |
| **建议修正** | 将 `host.auth.typed_metadata.v1`（或等价 driver/auth_type 枚举）列为 **M1 required 或明确降级为“人工标签 allowlist”**；无类型元数据时默认不自动过滤，由管理员配置 `include_auth_file_id`/`include_name_patterns`（精确/前缀策略需安全评审）。 |

### H7. 文档里程碑与 MVP 成功标准不一致，过约束实现顺序

| 项 | 内容 |
| --- | --- |
| **问题** | `00-overview`：M1 只读，M2 写，M3 检查/清理。`10-mvp-plan`：MVP 混入降权/启停，成功定义含自动降权；同时又说清理/套餐/Responses 为 MVP+。 |
| **位置** | `00-overview.md` §6；`10-mvp-plan.md` §1–5 |
| **为何高风险** | 评审通过后实现会按“成功定义”抢做降权，绕过契约冻结，放大 C1–C4。 |
| **建议修正** | 统一一张里程碑表：**M1 零写副作用**；任何 host 写不得进入 M1 验收。 |

### H8. 删除保护要求“已降权到 -100 / 已 unhealthy”等，存在操作死锁与错误激励

| 项 | 内容 |
| --- | --- |
| **问题** | strict：必须停用 + priority=-100 + 近 24h 无 usage 等；maximum 更要求健康 unhealthy。无 invoke 时无法刷新 health，可能**永远不能删**；或运维为删号先人为打失败/降权。 |
| **位置** | `03-domain-model.md` §9；`04-features.md` §10 |
| **为何高风险** | 过约束导致关闭保护档位或走宿主旁路删文件（文档禁止的捷径）。 |
| **建议修正** | 保护规则与“插件 demotion 魔法值”解耦：核心不可关门槛保持鉴权/精确身份/revision/确认令牌；`-100`/health 降为 **可配置建议项**，strict 默认“必须 disabled + 非最近活跃”，不要强制 demotion 状态机。 |

---

## Medium

### M1. 单管理密钥映射 `admin`：最小权限缺失

| 项 | 内容 |
| --- | --- |
| **位置** | `02-cpa-integration.md` §7.1 |
| **风险** | 只读运维与删除同权；误操作面大。 |
| **建议** | 角色模型保留；单一密钥时审计强制 `auth_method=management_key`，危险操作额外 confirmation；若 CPA 后续支持多密钥再映射角色。 |

### M2. `write_mode` 可在设置页切换，缺“切换门禁”

| 项 | 内容 |
| --- | --- |
| **位置** | `06-ui-ux.md` §6；`ADR/0003` |
| **风险** | 旧插件仍在写时误开 managed → 双写。 |
| **建议** | `read_only→managed` 需二次确认文案（确认无其他写责任方）+ 审计；可选冷却期。 |

### M3. 文件名回退主键：rename 语义与删除确认耦合

| 项 | 内容 |
| --- | --- |
| **位置** | `00-overview.md` §4.1；`03-domain-model.md` §2 |
| **风险** | rename 导致 usage 断层、baseline 孤儿；删除确认文本与 id 同为文件名时交叉校验增益下降。 |
| **建议** | 回退模式在 UI 标注“弱身份”；rename 检测后 tombstone 旧名；尽量推动宿主 stable id。 |

### M4. token 溢出“拒绝事件”可能引发重试死循环

| 项 | 内容 |
| --- | --- |
| **位置** | `03-domain-model.md` §1 |
| **风险** | 永久非法/溢出事件若返回可重试，宿主空转。 |
| **建议** | 溢出与 schema 非法 → **不可重试**错误 + 指标；仅 I/O 类可重试。 |

### M5. 停用“最后一个可用账号”规则待定

| 项 | 内容 |
| --- | --- |
| **位置** | `04-features.md` §8 |
| **风险** | 欠约束：实现要么误伤应急停用，要么放行导致全站无账号。 |
| **建议** | 默认 strict 拒绝全停用，提供 `force=true` + admin + 确认文本。 |

### M6. 审计 fail-closed 与 usage 路径可用性张力

| 项 | 内容 |
| --- | --- |
| **位置** | `04-features.md` §13；`07-persistence.md` §4 |
| **风险** | 审计盘满时若连 usage 也 fail-closed，可能影响宿主重试；若 usage 豁免则计量与审计分裂。 |
| **建议** | 危险写 fail-closed；usage 允许降级为“计数成功 + audit_dropped 指标”，但 demotion 不得在无审计时 applied。 |

### M7. 共存检测“能则检测”欠约束

| 项 | 内容 |
| --- | --- |
| **位置** | `ADR/0003`；`09-risks-and-open-questions.md` §2.9 |
| **风险** | 无检测时仅靠 runbook，现实中常被跳过。 |
| **建议** | 无宿主插件列表 API 时，`managed` 启动打印高亮 WARN 并在 UI 常驻横幅；文档列为发布门禁而非可选。 |

### M8. API/领域字段命名漂移（实现期一致性）

| 项 | 内容 |
| --- | --- |
| **位置** | `05-api-contracts.md` `missing_usage_events` vs `03` `eventsWithMissingUsage` 等 |
| **风险** | 中等：契约测试脆弱、前后端对不齐。 |
| **建议** | 单开 schema 源生成 DTO；设计文档附字段对照表。 |

---

# 五问最佳实践

## Q1. CPA 是否有稳定 `auth_file_id` + usage `event_id`？（没有时如何设计）

| 项 | 内容 |
| --- | --- |
| **推荐选择** | **硬依赖稳定 `auth_file_id`（宿主在选定 auth 后填入，rename 不变）+ 稳定 `event_id`（至少 host 范围内唯一，重放不变）+ at-least-once 可重试语义。** 二者都写入契约测试，作为 M1 精确计量 / M2 降权的门禁。 |
| **理由** | 多账号网关/代理类系统的行业惯例：计费与风控主键用 **opaque stable account id**，不用显示名；用量管道用 **idempotency key / event id** 做至少一次投递下的精确一次效果（Stripe/AWS 用量类接口同构）。插件侧无法从 `provider` 或请求内容可靠反推账号。 |
| **备选** | 仅有精确文件名：主键 = 规范化 `file_name`，rename = tombstone+新建；UI 标明弱身份。仅有弱 `event_id`：按 `(auth_id, request_id, occurred_at, token_tuple)` 合成幂等键并文档化碰撞风险。 |
| **CPA 能力不足时的降级** | 无可信身份：只展示 list 投影 + **global unattributed** 计数，**关闭**按账号降权/按账号清理关联。无稳定 event id：UI 显示“可能重计/漏计”，关闭“精确去重”验收；可提供运维“统计重置（审计）”而非假装精确。M1 仍可做只读列表，但不得承诺账本级计量。 |

## Q2. 失败归责怎么定？（CPA 字段 vs 保守 allowlist）

| 项 | 内容 |
| --- | --- |
| **推荐选择** | **优先 CPA 稳定字段**（如 `error.attribution=auth|client|host|upstream` + 稳定 `code`）。插件只消费枚举，不解析错误文案。字段缺失时用 **版本化保守 allowlist**（默认极小集：明确 invalid_grant / auth 文件级 401 等经契约确认的码），unknown **不计连续降权**。 |
| **理由** | SRE 实践：自动化处置（降权/熔断）必须 **fail-safe**，误报成本高于漏报。可观测性系统区分 *errors* 与 *attributable faults*；负载均衡 outlier detection 通常要求连续且同类失败，避免把取消、网关 429、客户端断开算进后端。 |
| **备选** | 无字段时纯 allowlist + 强制 `policy_version`；或“只统计不降权”人工处置模式。 |
| **降级** | 无可靠归责：`demotion_enabled=false`，仅展示 `failedRequests` 与原始 code 直方图；阈值降权入口隐藏。禁止用 HTTP 状态码大类（凡 4xx/5xx）一刀切。 |

## Q3. `priority=-100` 与条件写（revision）CPA 是否应依赖？

| 项 | 内容 |
| --- | --- |
| **推荐选择** | **条件写：应依赖（M2 强依赖）。** 降权目标：**依赖“宿主定义的最低/降级优先级”能力**，而不是字面 `-100`；若宿主文档明确负数与排序语义，再把 `-100` 作为默认 `demotion_priority` 常量。 |
| **理由** | 乐观并发（If-Match / revision）是控制面改配置的标准防踩踏手段；无条件写的多控制面（面板 + 旧插件 + 自动降权）必然 last-write-wins。优先级数值语义属于调度器，插件不应发明“魔法优先级”除非宿主保证排序。 |
| **备选** | 无 revision：仅人工单账号写 + 写前再读 + 冲突则失败；自动降权关闭。无通用 priority：用 `set_enabled(false)` 作为“硬隔离”（语义更强，需单独产品确认）。 |
| **降级** | 无条件写 ⇒ 自动降权 **禁止**，批量写默认关。无可靠 priority 语义 ⇒ 不实现 demotion，改为建议操作员停用；保护规则不引用 `-100`。 |

## Q4. 人工检查能否/应否锁定指定 auth？（无 invoke 怎么办）

| 项 | 内容 |
| --- | --- |
| **推荐选择** | **应当锁定。** 健康/套餐/Responses 必须通过 `host.auth.invoke` 或 **credential lease（短时、单 auth、不可调度替换）**，并校验返回的 `observed_auth_file_id == 目标`。 |
| **理由** | 安全与正确性：检查结果是对**特定凭据**的观察；账号池调度下的“裸请求”只能证明池子，不能证明个体。云上“assume role / impersonate”类 API 都要求显式身份，而不是“随机命中再猜”。 |
| **备选** | 宿主支持“sticky routing key”且 usage/响应回显 auth id：可接受，但要把回显字段升为 required。 |
| **降级** | 无 invoke/lease/回显：相关 operation API **整组 capability_unavailable**；不提供半吊子探测。运维改用 CPA 原生工具做单账号调试。绝不读取 auth 文件秘密。 |

## Q5. MVP 砍法：先只读+usage+降权，还是一上来清理/套餐/Responses？

| 项 | 内容 |
| --- | --- |
| **推荐选择** | **严格分阶段：M1 = 只读列表 + usage 累计 + 持久化 + 数据质量（零 host 写副作用）→ M2 = 条件写证实后的降权/启停/恢复 → M3 = invoke 证实后的检查 → 清理最后（能力+保护规则全齐）。** 即：**先只读+usage，降权紧随但受门禁；不要一上来清理/套餐/Responses。** |
| **理由** | 插件/控制面常见切片：先 *observe* 再建 *actuate*。删除与付费探测是最大 blast radius；降权次之但仍改调度。先验证身份与 usage，才能信任后续自动化。与 `00-overview` 里程碑一致，也符合能力协商下的可发布子集。 |
| **备选** | “只读+usage+**手动**降权/启停（无自动阈值）”作为 M1.5，若业务强依赖隔离可更快止损。 |
| **降级** | 契约未齐：发布 **read_only 商店灰度**（M1 产物），registry 标注 partial capabilities。自动降权、清理、Responses 全部 feature-flag off，避免半实现。 |

---

# 总体结论（是否可进入 M1、需先冻结什么）

**可以进入 M1，但仅限“契约探测 + 只读骨架”含义上的 M1；不能把自动降权/清理/检查当作 M1 或“第一可用版本”的必达项。**

设计在安全哲学上成熟（管理鉴权、精确身份、确认令牌、不碰 auth 秘密、capability 降级），clean-room 与共存 ADR 清晰。当前 **不建议冻结“可自动改调度”的实现**，直到下列阻断项有书面答案与契约测试夹具：

### 进入 M1 前必须冻结（门禁）

1. **目标 CPA 版本/ABI 来源**与 capability 探测结果（真实有/无，而非目标 JSON）。  
2. **`auth_file_id` 稳定性**（含 rename）及 list 中 **xAI OAuth 类型字段**。  
3. **`usage.event_id` 唯一性范围、重试、乱序、ACK 语义**；落盘必须 **先持久化再成功确认**（否定“ACK 后 debounce 可丢”）。  
4. **M1 范围写死为零 host 写副作用**；成功标准与 `10-mvp-plan` 对齐 overview，去掉“第一可用=自动降权”。  
5. **去重结构**：精确集合策略与窗口满行为（禁止 FP bloom 作为账本去重）。

### 进入 M2 前必须冻结

6. **`set_priority` 合法范围与降级语义**（废除散落魔法 `-100`，改为宿主证实的 `demotion_priority`）。  
7. **`conditional_write` / revision**；无则自动降权不做。  
8. **失败归责字段或 v1 allowlist + `policy_version`**；探测失败是否计入降权（推荐默认否）。  
9. **降权状态机与异步执行**，禁止 usage 热路径双写分裂（C2）。  
10. **灰度单一写责任** runbook/UI 门禁（含 `write_mode` 切换确认）。

### 进入 M3 前必须冻结

11. **`host.auth.invoke` 或等价 lease + 实际 auth 回显**。  
12. **delete 能力形态**（精确 id+文件名+鉴权）；缺失则清理永不进发布。  
13. **保护规则与 demotion 魔法值解耦**，避免删号死锁。

**一句话：** 文档已具备 M0→M1（只读+usage）的设计质量；**Critical 项（身份/event_id、降权事务边界、`-100` 语义、自动降权门禁）未冻结前，不得进入 M2 实现，更不应以清理/套餐/Responses 拉动首发。** 先钉死宿主契约矩阵，再开写路径。
