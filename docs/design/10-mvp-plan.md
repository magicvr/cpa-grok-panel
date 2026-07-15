# 10. MVP 计划

## 1. MVP 目标

在 **不抄旧代码** 的前提下，先交付可安装、可验证且零 auth 写副作用的 `cpa-grok-panel`（Linux amd64）M1：

- 列表观察 xAI OAuth 账号
- 真实 usage 累计
- `auth_index` + 精确 name 的只读账号投影
- 插件 `usage.handle` 真实计量与 exact/weak 去重状态
- state 持久化、设置读取与只读 UI
- `write_mode=read_only`，除插件自身配置外不产生 auth 写
- 基础测试与 Release

status 启停、fields 降权/恢复进入 M2；检查与安全删除进入 M3。第一可用版本不以自动降权为必要条件。

## 2. 分期

### M0 设计冻结（当前）

- [x] Clean-room 设计文档
- [x] 按 2026-07-15 实测冻结身份、usage、写能力五问
- [ ] 确认 CPA 版本矩阵

### M1 只读骨架

- 插件 register / 能力协商
- Management auth-files list 双键投影
- usage 累计 + state 持久化
- 管理 API：meta、accounts、settings(read)
- UI：概览 + 列表
- write_mode 固定 read_only

**验收：** 列表正确；exact 模式重放不双计，weak 模式明确不承诺账本级；除插件自身配置外无 auth 写副作用。

### M2 受控写

- fields priority 异步降权 + baseline
- status 批量启停
- restore_priority
- operation 模型 + 审计
- write_mode=managed

**验收：** 所有写项携带 `auth_index` + `exact_file_name`；降权不阻塞 usage handler；无 revision 时串行写、写后 re-list、防抖和 LWW 行为符合契约；部分失败可见。

### M3 检查与清理（按能力）

- 健康检查
- 套餐核实
- Responses 实测
- cleanup 双确认（可先单账号；MVP 可不做）

**验收：** 无安全 invoke/lease 时检查整组保持 unavailable；保护规则挡得住误删，删除后 re-list；检查不泄漏秘密。

### M4 商店灰度

- registry + Release + checksums
- 与旧 grok-panel 并存 runbook
- 回滚演练

## 3. MVP 明确砍掉

- 自动检查 / 自动删除
- token 估算
- 多平台构建
- 旧插件 state 迁移
- 复杂图表与移动端

## 4. 建议讨论拍板项（进入 M1 前）

1. CPA 目标版本与 ABI 来源（文档/SDK 路径）
2. 插件显示名与菜单文案
3. 仓库公开/私有、Release 权限
4. 是否 M1 就上 UI，还是先 CLI/API 自测
5. 失败阈值与降权目标是否可配

第 5 项已冻结：阈值可配，`demotion_priority` 可配，默认候选 `-100`。

## 5. 成功定义（第一可用版本）

运维可以：

1. 打开面板看到 xAI 账号与真实 token
2. 清楚看到统计起点、数据完整性和 exact/weak 去重模式
3. 在零 auth 写副作用下与旧插件并存验证
4. 出问题可移除/停用新插件而不丢 CPA auth 真相

## 6. 下一步（讨论后）

- 以冻结契约编写 M1 实现任务拆分
- 为 raw usage `event_id` 和插件 host invoke 做后续能力探测
- M1 验收后再启用 M2 写责任切换评审
