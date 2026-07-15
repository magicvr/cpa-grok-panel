# CPA 能力实测报告（设计门禁）

- 日期：2026-07-15
- 目标：`CPA_BASE_URL`（内网实例）
- 方法：Management REST 探测 + 已加载 `grok-panel` 的 `usage-debug`
- 密钥：仅使用环境变量，未写入本文件

## 1. 结论总表（对照设计五问）

| 设计需要 | 实测结果 | 对 clean-room 的含义 |
| --- | --- | --- |
| 稳定账号身份 | **有 `auth_index`（16 hex）**；写操作 **必须用精确 `name`（*.json）**；`id` 与 `name` 相同 | Q1：身份可用，但 **双键模型**（index 归因 + name 写入） |
| usage 事件 / 失败回调 | **有**（已注册插件 `usage-debug` 可见 `failed/demoted/status_code/provider`） | usage 热路径存在；**未见独立 `event_id` 字段暴露** |
| 失败归责 | debug 里有 `status_code`（如 401）、`provider`、`executor_type`；无正式 `attribution` 枚举 | Q2：应用 **allowlist（401/403…）**，不能等完美 attribution |
| `priority` 读写 | 列表含 `priority:int`；`PATCH /auth-files/fields` **可用**（`name`+`priority`） | Q3：可降权；**无 revision 条件写** |
| 启停 | `PATCH /auth-files/status` **可用**（`name`+`disabled`） | M2 启停可行 |
| 删除 | `DELETE /auth-files` 路由存在（不存在文件 → `auth file not found`） | 清理能力具备；需精确 name |
| 条件写 revision | **字段不存在** | Q3：禁止依赖 If-Match；要防抖/串行/接受 LWW |
| 锁定指定 auth 检查 | Management REST **无** invoke/lease 证据 | Q4：检查须另证（插件 host 回调/直连凭据），不能假设 invoke |
| 插件体系 | `plugins.enabled=true`，`grok-panel` registered+enabled | 可灰度并存新 id |
| 管理 usage 统计 HTTP | `/v0/management/usage` **404**；config `usage-statistics-enabled=false` | 面板计量应靠 **usage 插件回调**，不靠管理 usage API |

## 2. 已证实可用的 Management API

### 2.1 列表

`GET /v0/management/auth-files` → 200

样本字段（xAI OAuth，约 598 个文件）：

- 身份：`auth_index`, `name`, `id`（=name）, `path`
- 类型：`provider=xai`, `type=xai`, `account_type=oauth`
- 状态：`disabled`, `status`, `status_message`, `unavailable`
- 调度：`priority`（int）
- 用量旁路：`success`, `failed`, `recent_requests[{time,success,failed}]`
- **无**：`revision`

### 2.2 启停

`PATCH /v0/management/auth-files/status`

```json
{"name":"<exact.json>","disabled":false}
```

→ 200 `{"status":"ok","disabled":false}`

### 2.3 改字段（含 priority）

`PATCH /v0/management/auth-files/fields`

```json
{"name":"<exact.json>","priority":0}
```

→ 200 `{"status":"ok"}`

约束实测：

- **必须** `name`（精确文件名）
- 仅 `auth_index` / `names[]` → 400 `name is required`
- 非法 name → 404 `auth file not found`（表示路由存在）

### 2.4 删除

`DELETE /v0/management/auth-files`（body 或 query `name=`）

不存在文件 → 404 `auth file not found`（路由存在；**未对真实账号执行删除**）

### 2.5 插件

`GET /v0/management/plugins` → 200

- `plugins_enabled: true`
- 已加载：`grok-panel` v 路径 `plugins/linux/amd64/grok-panel-v1.1.33.so`，registered+enabled

`GET /v0/management/plugins/grok-panel/usage-debug` → 200

可见真实失败降权摘要示例字段：

- `auth_index`, `auth_id`/`name`, `provider`, `auth_type`, `executor_type`
- `failed`, `status_code`, `demoted`, `priority`, `reason`

说明：**usage.handle 链路在当前 CPA 上是通的**（至少对已加载插件）。

## 3. 未证实 / 不支持（REST 视角）

| 项 | 结果 |
| --- | --- |
| `GET /v0/management/version` | 404 |
| `GET /v0/management/usage` | 404 |
| OpenAPI/Swagger | 404 |
| REST 级 `expected_revision` | 无字段 |
| REST 级 auth invoke / sticky | 未发现 |
| 全局稳定 `event_id` | usage-debug **未暴露**；需插件侧抓 raw usage 或源码契约另证 |

## 4. 对五问的实测答案

### Q1 身份 + event_id

- **身份：支持（双键）**  
  - 归因/匹配：`auth_index`  
  - 写入：精确 `name`  
- **event_id：REST 未证明**  
  - 设计应：有则精确去重；无则弱一致 + 不承诺账本级，或插件生成合成键并文档化碰撞风险

### Q2 归责

- 有 HTTP `status_code` + provider/type  
- **无**正式 attribution 枚举  
- 推荐：**版本化 allowlist**（至少 401/403；429/5xx 是否计入另定）

### Q3 priority=-100 + revision

- **priority 可写**：`PATCH fields`  
- **revision：不支持**  
- 推荐：可配置 `demotion_priority`（默认 -100 可先实测调度效果）；自动降权必须 **串行+防抖+接受 LWW**，不能假装 CAS

### Q4 检查锁 auth

- Management REST **不能**证明 invoke  
- 检查能力要么走插件 `host.auth.get` 取凭据后直连（旧路径），要么等宿主能力  
- clean-room：**无锁定则检查功能整组 optional**

### Q5 MVP 砍法

结合实测：

1. **M1 立刻可做**：list 投影 + 设置 + 只读 UI（598 账号字段够用）  
2. **M2 可做**（无 revision 的受控写）：status 启停、fields 降权/恢复（精确 name）  
3. **usage 累计/自动降权**：宿主已能推 usage（旧插件已 demote）→ 新插件可做，但要处理 **无 event_id / 无 revision**  
4. **清理**：DELETE 路由在，放 M3 + 双确认  
5. **套餐/Responses**：与 REST 无关，依赖检查路径，后置

## 5. 设计修订建议（高优先级）

1. 领域主键：`AuthIndex`；写端口强制 `ExactFileName`  
2. 删除 `conditional_write` 强依赖；改为 **optional**，缺失时策略=串行写+冲突检测（写后 re-list）  
3. usage 去重：探测 raw 是否含稳定 id；否则 `weak_dedupe` 模式  
4. 自动降权默认允许（宿主已证明可 demote），但必须异步队列，避免 usage 热路径阻塞  
5. 新插件 id `cpa-grok-panel` 可与现 `grok-panel` 并存；灰度时 **只开一个写责任方**

## 6. 安全说明

- 本次对真实文件的写探测仅为 **no-op**（disabled/priority 保持原值）  
- **未**删除任何 auth 文件  
- 报告不含管理密钥与 token
