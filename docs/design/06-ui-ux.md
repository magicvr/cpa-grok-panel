# 06. UI/UX

## 1. 目标

管理面板让运维人员在 CPA 管理鉴权上下文中完成观察、人工检查和受控写操作。UI 只消费本插件管理 API，不直接调用 CPA host auth 接口，不在浏览器保存管理密钥。

## 2. 信息架构

建议三个主视图：

| 视图 | 职责 |
| --- | --- |
| 概览 | 账号总数、启用/停用、真实 token 总量、数据质量、写模式、不可用能力 |
| 账号管理 | 列表、筛选、批量操作、单账号详情/危险操作 |
| 设置 | 并发、失败阈值、保护档位、token 容量、写模式说明 |

可选：操作中心（最近 operations）、审计只读。

## 3. 账号列表列（MVP）

| 列 | 说明 |
| --- | --- |
| 选择 | 多选 |
| 文件名 | 精确 `file_name` |
| 启用 | enabled |
| 优先级 | host priority；降权态高亮 |
| 健康 | status + stale 标记 |
| 套餐 | tier + confidence |
| Token | total / capacity / ratio |
| 失败 | 连续可归责失败 / 阈值 |
| 操作 | 检查、套餐、Responses、启停、恢复、清理入口 |

排序建议：优先级、token、失败次数、最后活动时间。筛选：启用、健康、套餐、降权状态、搜索（仅显示字段）。

## 4. 批量栏

- 选择本页 / 清除选中（有选中才显示）
- 检查选中 / 核实套餐 / Responses 实测
- 启用 / 停用 / 恢复优先级
- 不提供无保护的“一键删除选中”；清理走危险区两阶段

## 5. 反馈与 busy 生命周期

1. 用户触发操作 → 创建 operation（或同步错误）
2. UI 进入 busy，绑定 `operation_id` 与 generation token
3. 轮询 `GET /operations/{id}` 直到终态
4. 展示汇总 + 首个失败原因；刷新列表
5. 旧 operation 的回调不得覆盖新操作反馈

规则：

- 同时只允许一个写/检查 busy（或明确队列）
- 刷新数据不得静默清掉当前操作反馈
- 超时后可取消 queued；写操作不宣称已回滚

## 6. 设置页

可编辑：

- operation concurrency
- attributed failure threshold
- protection level
- default token capacity
- per-account capacity（可后期）
- health stale window
- operation timeout
- write_mode（只读/受管，灰度关键）

不可出现：自动检查、自动删除、每请求估算 token。

## 7. 安全与可访问性

- 删除确认使用精确文件名确认文本，不回显密钥
- 危险按钮二次确认 + 明确后果文案
- 键盘可操作；状态不只靠颜色
- 不加载第三方脚本；CSP 限制性策略

## 8. 空态与错误

- host 不可用：显示上次成功快照时间 + 禁用写
- 无 xAI 账号：引导检查 CPA auth
- capability 缺失：对应按钮 disabled + reason
- 统计起点：明确“本插件启用后开始累计”

## 9. 非目标

- 不做复杂可视化大屏
- 不做移动端优先（桌面运维即可）
- 不做暗黑/主题系统作为 MVP 阻断项
