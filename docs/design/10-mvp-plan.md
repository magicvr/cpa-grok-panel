# 10. MVP 计划

## 1. MVP 目标

在 **不抄旧代码** 的前提下，交付可安装的 `cpa-grok-panel`（Linux amd64），先具备：

- 列表观察 xAI OAuth 账号
- 真实 usage 累计
- 失败自动降权（在 CPA 契约允许时）
- 手动健康检查（若 invoke 能力可用，否则延后）
- 批量启停 / 恢复优先级（若 host 写能力可用）
- 设置与 write_mode 灰度
- 基础测试与 Release

安全清理、套餐核实、Responses 实测可作为 MVP+。

## 2. 分期

### M0 设计冻结（当前）

- [x] Clean-room 设计文档
- [ ] 评审开放问题中的阻断项（身份、usage、写能力）
- [ ] 确认 CPA 版本矩阵

### M1 只读骨架

- 插件 register / 能力协商
- host.auth.list 投影
- usage 累计 + state 持久化
- 管理 API：meta、accounts、settings(read)
- UI：概览 + 列表
- write_mode 固定 read_only

**验收：** 列表正确；usage 重放不双计；无写副作用。

### M2 受控写

- set_priority 降权 + baseline
- set_enabled 批量启停
- restore_priority
- operation 模型 + 审计
- write_mode=managed

**验收：** 降权/恢复/启停幂等与冲突行为符合契约；部分失败可见。

### M3 检查与清理（按能力）

- 健康检查
- 套餐核实
- Responses 实测
- cleanup 双确认（可先单账号）

**验收：** 保护规则挡得住误删；检查不泄漏秘密。

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
5. 失败阈值与 -100 默认值是否写死可配

## 5. 成功定义（第一可用版本）

运维可以：

1. 打开面板看到 xAI 账号与真实 token  
2. 失败账号被自动降到 -100 且可手动恢复  
3. 批量停用问题账号  
4. 出问题可切回只读/旧插件而不丢 CPA auth 真相  

## 6. 下一步（讨论后）

- 冻结 `docs/design` 中的阻断开放问题答案  
- 生成 `internal/` 空模块与接口桩（仍无业务抄袭）  
- 对接真实 CPA 做契约探测实验  
