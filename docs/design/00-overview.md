# 00. 设计总览

## 1. 背景

CPA 可管理多个 OAuth auth 文件并代理模型请求。账号数量增多后，运维人员需要知道哪些 xAI 账号可用、实际消耗多少 token、当前优先级为何，以及如何安全地批量停用、恢复或清理账号。

`cpa-grok-panel` 的定位是 CPA 内的运维插件：读取 CPA 暴露的 auth 元数据，接收 usage 事件，执行经过鉴权和保护规则约束的管理动作，并提供轻量管理面板。

## 2. 目标

- 统一展示 xAI OAuth 账号的启用状态、健康状态、套餐、优先级、真实 token 用量和最近活动。
- 可靠地把 usage 与具体 auth 文件关联，并按账号累计真实输入、输出及总 token。
- 在明确归属于某 auth 的失败达到阈值后，可按配置将对应文件优先级异步降至 `demotion_priority`（默认候选 `-100`），同时保留可恢复的基线值。
- 提供人工触发的健康检查、套餐核实和 Responses 实测。
- 提供受保护的批量启用、停用、恢复优先级及安全删除。
- 提供并发数、失败阈值、保护档位、每账号 token 上限等设置。
- 为 Linux amd64 的 GitHub Release 与 CPA registry 安装方式建立可测试的发布流程。

## 3. 非目标

- 不实现 CPA 请求路由、负载均衡、OAuth 登录或 token 刷新。
- 不解析和改写 auth 文件内部秘密；只通过 CPA 已证实的 Management API 或正式插件 host 能力操作。
- 不依据 `provider` 文本推断失败账号；缺少可信 auth 文件身份时不降权。
- 不估算单次请求 token；usage 缺字段时不补算。
- 不自动运行健康检查、套餐核验或 Responses 测试。
- 不自动删除、自动恢复优先级或自动启用账号。
- 不成为账单系统；token 上限是运维容量分母与告警依据，不代表供应商结算额度。
- 不保证绕过 CPA 的写操作能被正确观察；所有受管变更应走 CPA Management API。

## 4. 核心约束

### 4.1 身份

领域归因主键 `AccountID` 取 CPA 的稳定 `auth_index`。所有 Management 写端口同时强制携带 `ExactFileName`，其值必须是当前列表返回、以 `.json` 结尾的精确 `name`。这是双键模型：`auth_index` 用于 usage 匹配和领域状态，`name` 用于 status、fields 与 delete；显示名、邮箱、provider 均不是主键。重命名后必须重新 list 并刷新 index→name 映射，禁止用旧文件名猜测写目标。

### 4.2 计量

只接受插件 `usage.handle` 回调中的真实 `input_tokens`、`output_tokens`、`total_tokens`，不依赖不存在的 Management usage HTTP。事件缺失 token 时只更新请求/失败统计及数据质量标记。宿主提供稳定 `event_id` 时精确去重；否则进入 `weak_dedupe`，明确不承诺账本级精确性。不得调用 tokenizer 推算。

### 4.3 写操作

当前已证实的写路径是 CPA Management REST：`PATCH /v0/management/auth-files/status`、`PATCH /v0/management/auth-files/fields` 与 `DELETE /v0/management/auth-files`，均以精确 `name` 定位。插件不得直接写 auth 目录。列表无 revision，因此 M2 写入采用单账号串行、写前/写后 re-list 校验、防抖和接受 last-write-wins；不得伪装成 CAS，也不得让自动降权依赖 revision。

### 4.4 人工检查

健康、套餐和 Responses 检查由管理员显式触发，且仅在宿主提供锁定指定 auth 的正式安全路径时可用。系统可展示结果过期，但不后台刷新。

## 5. 成功标准

- usage 事件在具有可信 `auth_index` 时归账；精确模式下重放不重复累计，`weak_dedupe` 模式明确展示其精度边界。
- M1 只读列表、usage 累计、state 与只读 UI 可独立交付，零 auth 写副作用。
- M2 失败降权从“事件归因”到“异步队列写优先级”全链路可测试、可审计、可恢复，且不阻塞 usage handler。
- 删除操作无法通过模糊匹配、显示名或 provider 发起。
- 任一批量操作都返回逐账号结果，部分失败不会伪装成全部成功。
- UI 刷新或请求超时后能够重新获取最终状态，不永久卡在 busy。
- state 写入支持并发、崩溃安全和 schema 迁移；损坏时可诊断且不静默清空。
- 新旧插件可安装并存，数据目录、路由及资源不冲突。
- Linux amd64 产物可由 Release 校验和验证，并能按兼容矩阵安装和回滚。

## 6. 里程碑

| 里程碑 | 结果 | 退出条件 |
| --- | --- | --- |
| M0 设计冻结 | 契约、模型、安全边界评审完成 | 开放问题中的阻断项有明确决定 |
| M1 只读 MVP | list、usage 累计、state、只读 UI（仅配置可写） | 零 auth 写副作用；精确/弱去重模式可辨识 |
| M2 受控写操作 | status 启停、fields 降权/恢复 | 精确 name、串行写、写后校验、审计和部分失败验证通过 |
| M3 检查与清理 | 有安全锁定路径时的检查；双确认删除 | 无 invoke 时整组禁用；删除保护和结果复核通过 |
| M4 商店灰度 | Release、registry、共存 | Linux amd64 灰度和回滚演练完成 |

## 7. 设计原则的验收方式

- “不替代 CPA”：领域与 UI 不直接访问 auth 文件系统。
- “只信真实 token”：代码审查规则禁止 tokenizer/估算逻辑进入 usage 路径。
- “默认人工”：没有定时器或后台巡检入口。
- “避免屎山”：模块依赖由架构测试约束；UI 状态、API client、视图拆分。
- “安全清理”：四道门槛——管理鉴权、精确身份、保护规则、确认令牌。
