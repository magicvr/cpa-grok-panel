# cpa-grok-panel

`cpa-grok-panel` 是面向 CLIProxyAPI（下称 CPA）的 Grok/xAI OAuth 账号运维面板插件设计项目。它计划提供账号清单、真实 token 用量累计、失败降权、人工健康与套餐核验、批量状态操作、安全清理和运维设置等能力。

## 当前阶段

本仓库当前处于 **设计阶段**。仓库只包含需求边界、架构、接口契约、领域模型、交互规范、持久化方案、测试与发布计划；不包含可运行的插件、服务端或前端业务实现。

进入实现阶段前，必须先确认 CPA 实际插件 SDK/ABI、管理 API、认证模型与 usage 回调字段。本设计中未被 CPA 官方契约证实的部分均视为“目标契约”，不得直接当成已存在能力。

## 项目定位

- 面向 Grok/xAI OAuth auth 文件的运维可视化和安全操作入口。
- 以 CPA `usage` 回调提供的真实计数累计 token，不估算每请求 token。
- 以 CPA 识别的 auth 文件身份作为账号主键和失败归因依据，不依赖不可靠的 `provider` 字符串。
- 默认只做人工健康检查、人工套餐核验和人工 Responses 实测。
- 通过 CPA 提供的 auth 管理能力修改启用状态和优先级，不替代 CPA 核心调度。

## 与旧插件的关系

本项目采用 clean-room 方式独立设计，插件 id 拟定为 `cpa-grok-panel`。它不是旧 `grok-panel` 或任何 `cpa-plugin-grok-panel` 的拷贝、分支或改写，不读取、不复用其源码、私有接口或实现细节。

新旧插件可在灰度期并存，但必须使用不同插件 id、管理路由前缀、静态资源前缀和状态目录。并存仅用于迁移验证；同一 auth 文件的写操作应由单一插件负责，避免相互覆盖优先级或禁用状态。

## 设计原则

1. **安全优先**：删除要求管理密钥、精确文件名、保护规则和二次确认共同成立。
2. **真实计量**：只累计 CPA usage 的真实 token 字段；缺失时记录“不完整”，不推算。
3. **人工触发**：不做自动健康检查、自动套餐核验、自动删除。
4. **职责清晰**：CPA 负责认证文件与请求调度；插件负责观察、编排和受控管理操作。
5. **模块隔离**：适配、领域、应用服务、持久化、HTTP、UI 分离，禁止单文件堆叠 UI 与业务逻辑。
6. **可回滚**：状态变更可审计；失败降权保存可恢复基线；灰度和旧插件互不污染。

## 文档导航

- [设计总览](docs/design/00-overview.md)
- [分层架构](docs/design/01-architecture.md)
- [CPA 集成契约](docs/design/02-cpa-integration.md)
- [领域模型](docs/design/03-domain-model.md)
- [功能规格](docs/design/04-features.md)
- [API 契约](docs/design/05-api-contracts.md)
- [UI/UX](docs/design/06-ui-ux.md)
- [持久化](docs/design/07-persistence.md)
- [测试与发布](docs/design/08-testing-release.md)
- [风险与开放问题](docs/design/09-risks-and-open-questions.md)
- [MVP 计划](docs/design/10-mvp-plan.md)
- [架构决策记录](docs/design/ADR/)

## 明确不做

- 不替代或绕过 CPA 的核心请求调度、OAuth 刷新、认证文件解析和密钥校验。
- 不根据请求内容、本地 tokenizer 或响应长度估算 token。
- 不后台轮询健康、套餐或容量，不自动删除账号。
- 不把套餐核验结果当作强一致授权事实。
- 不在浏览器保存 CPA 管理密钥，不将密钥写入日志或 state 文件。
- 不承诺兼容未经契约测试的 CPA 版本。

## 目标平台与分发

首要目标平台是 Linux amd64。计划通过 GitHub Release 提供带校验和的版本产物，并由 `registry.json` 描述插件 id、版本、平台资产和兼容范围。具体格式必须在实现前与 CPA 商店规范对齐。

## 状态

设计基线：`v0-design`。实现、发布与运行说明将在设计评审通过后补充。
