# 11. Soft demotion + half-open 恢复（v0.5.0 草案）

状态：draft for implementation  
版本目标：`0.5.0`  
范围：插件侧 priority-only 调度升级 + 面板布局贴合 CPA 内页 padding

## 1. 目标

减少「持续/间歇报错 auth」被 CPA 反复选中导致的尾延迟，同时保留现有硬降权护栏与可恢复性。

## 2. 非目标

- 不实现 CPA 内核路由、hedge、bandit 选路
- 不自动 `disabled` / 删除
- 不在 `usage.handle` 内同步 Management PATCH

## 3. 基线（v0.4.2）

- 401/403 计入连败；≥ `attributed_failure_threshold`（默认 3）→ `demotion.requested` → worker 写 `priority=demotion_priority`（默认 -100）
- 成功/非归因失败 → **清零**连败
- 冷却 `6h→12h→24h` 后一步恢复 baseline
- 面板 `.wrap{width:100%;padding:24px 18px 36px}`

## 4. 算法（P0 必须实现）

### 4.1 双轨证据

| 轨 | 信号 | 行为 |
|---|---|---|
| **Hard streak** | 现有 `ConsecutiveAttributedFailures` | 归因失败 +1；**仅 hard 成功路径见下** |
| **Debt score** | 新字段 `failure.debt_score`（float64，≥0） | 401/403：`+debt_fail_401`（默认 1.5）；可选 429：`+debt_fail_429`（默认 0.5，仅 `count_status_429`）；成功：`max(0, score-debt_success_decay)`（默认 1.0）；非归因失败：**不变**（不再清零 debt） |

Hard streak 变更：
- 归因失败：仍 +1
- **成功**：`consecutive = 0`（hard 连败清零），但 **debt 只衰减不清空**
- 非归因失败：consecutive 清零；debt 不变

### 4.2 档位与目标 priority

| Class | 进入条件 | Target priority | demotion.state |
|---|---|---|---|
| `none` | 默认 | host baseline | `none` |
| `soft` | `debt_score >= soft_debt_threshold`（默认 2.0）且当前 class 非 hard/half_open 的 hard 隔离 | `soft_demotion_priority`（默认 **-10**） | `requested`→`applied`，`class=soft` |
| `hard` | `consecutive >= attributed_failure_threshold` **或** `debt_score >= hard_debt_threshold`（默认 4.5） | `demotion_priority`（默认 **-100**） | 同现有，`class=hard` |
| `half_open` | hard 冷却到期后的恢复步骤 | `soft_demotion_priority`（观察档） | `applied` 或专用 `half_open`，`class=half_open` |

规则：
- hard 优先于 soft：一旦 hard 触发，覆盖 soft 目标
- 已 `requested/applied` 且 class=hard 时：usage 路径不重复入队 soft；hard 已 applied 时失败可更新 `last_failure_*` 与 debt，但不重复 requested（现有行为）
- soft 已 applied 时若达到 hard：升级为 hard requested
- baseline：首次 soft/hard 降权前保存 host 当前 priority（与现逻辑一致）

### 4.3 恢复（半开）

当 `cooldown_restore_enabled`：
1. hard（或 soft 若也设了 cooldown）到期：
   - **不要**直接写 baseline
   - 写 `priority = soft_demotion_priority`，`class=half_open`，记录 `half_open_since`
   - 保留 `baseline_priority` 与冷却阶梯计数
2. half_open 期间：
   - 归因失败（计 hard 的状态码）→ 立即 hard demote，阶梯冷却递增
   - 成功 → `half_open_successes++`；当 ≥ `half_open_success_threshold`（默认 **2**）→ 恢复 baseline，`class=none`，清空 debt/streak/half_open 计数（诊断 last_failure 可保留或清，与现 restore 一致可清空 failure）
3. bot 账号：仍不自动恢复（现逻辑）

Soft-only 降权（从未 hard）是否走冷却：
- P0：soft 也可被冷却 worker 处理——若 `class=soft` 且开启冷却，到期进入 half_open 同 hard；若实现成本高，**至少 hard 走 half_open**，soft 可人工恢复或同样 half_open（优先两者一致）

### 4.4 Worker

- 继续异步 `DemotionWorker`：根据 `target_priority` PATCH fields
- `CooldownRestoreWorker`：调用升级后的 `RestorePriorityAfterCooldown` / 新 `EnterHalfOpen` / `CompleteHalfOpen`
- 尽量收敛到单一「调度决策」函数，避免 demotion/restore 互相覆盖无审计

### 4.5 设置项（settings + UI 设置页）

新增（均有默认，持久化 state）：

| 字段 | 默认 | 说明 |
|---|---:|---|
| `soft_demotion_enabled` | true | 总开关 |
| `soft_demotion_priority` | -10 | soft/half_open 观察档 |
| `soft_debt_threshold` | 2.0 | 进入 soft |
| `hard_debt_threshold` | 4.5 | debt 触发 hard（与连败 hard 并行） |
| `debt_fail_401` | 1.5 | 401/403 加分 |
| `debt_fail_429` | 0.5 | 仅 count_429 时 |
| `debt_success_decay` | 1.0 | 成功减分 |
| `half_open_enabled` | true | 冷却后观察档 |
| `half_open_success_threshold` | 2 | 观察档成功次数回 baseline |

旧字段保留：`attributed_failure_threshold`、`demotion_priority`、`cooldown_restore_enabled` 等。

### 4.6 领域字段

`FailureState` 增加：
- `DebtScore float64`
- `LastEvidenceAt *time.Time`（可选）

`DemotionState` 增加：
- `Class string` // `none|soft|hard|half_open`
- `HalfOpenSuccesses int`
- 兼容：旧 state 无 class 时，`applied`+priority≈demotion_priority → class=hard

API `AccountView` 暴露 debt_score、class，便于面板诊断列。

### 4.7 测试（必须）

- 成功衰减 debt 不清空历史到 0 以外的中间值路径
- debt 达 soft → soft priority 写入
- 连败达 N → hard -100
- soft 升级 hard
- half_open 成功×阈值 → baseline
- half_open 失败 → hard + 冷却递增
- 回归：现有 demotion/cooldown/usage 测试更新期望

## 5. UI

### 5.1 CPA 内页布局套壳

参考 CPA 管理内页：在面板主体外增加容器：

```html
<div class="cpa-page-shell" style="/* or CSS class */">
  <main class="wrap">...</main>
</div>
```

CSS 要求：
- `.cpa-page-shell`：`margin: 0`；`padding-top: 70px`；`padding-right: 34.5px`；`padding-bottom: 40px`；`padding-left: 34.5px`；`box-sizing: border-box`；宽度 100%
- `.wrap`：**去掉**自身用于页边距的 padding/margin（当前 `padding:24px 18px 36px`）；保留 `width:100%`；内部组件间距用更小的内边距即可（如 header/toolbar 自有 gap，不必再套一层大 padding）
- 移动端 `@media`：可略减 shell padding（例如左右 16px、顶 24px），避免小屏浪费；**桌面默认必须是用户给定的 70/34.5/34.5/40**

### 5.2 诊断展示（小改）

- 诊断列或 hover 增加 `debt` / `class`（soft|hard|half_open）
- 版本号 → **v0.5.0**

## 6. 版本与文档

- `PluginVersion` / registry / panel subtitle / tests / README 关键版本 → **0.5.0**
- README 功能表增加：soft 降权、失败债务、half-open 恢复一行说明

## 7. 实现注意

- 无 `auth_index` 不动作
- 写仍 Management fields + 写后 re-list
- settings merge/env 冷启动兼容
- 不要破坏 store schema 迁移：缺省字段零值安全
- 提交清晰 commit；**不要 push**

## 8. 验收清单

- [ ] `CGO_ENABLED=1 go test ./...`
- [ ] `go vet ./...`
- [ ] soft/hard/half_open 单测覆盖
- [ ] panel：shell padding 正确，`.wrap` 无旧页边距
- [ ] 版本 0.5.0
