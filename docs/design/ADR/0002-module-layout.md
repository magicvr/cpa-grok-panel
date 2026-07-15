# ADR 0002：模块与目录布局

- 状态：拟接受，待实现评审确认语言与 UI 工具链
- 日期：2026-07-15

## 背景

插件同时涉及 CPA ABI、领域规则、并发操作、状态持久化、管理 API 和 UI。若按技术便利混写，失败降权、删除保护和鉴权容易散落在 handler 或页面中。

## 决策

实现阶段采用端口与适配器风格，拟定目录如下。此处仅规定职责，不代表已经创建可运行模块。

```text
cmd/                       # 极薄的构建入口（若 CPA ABI 需要）
internal/
  cpaadapter/              # register/reconfigure/usage/host DTO 转换
  domain/                  # 模型、不变量、纯规则
  application/             # 用例编排、操作生命周期
  ports/                   # AuthHost、Repository、CheckClient、Clock 等接口
  infrastructure/
    state/                 # 快照、迁移、锁、原子写
    xai/                   # 人工检查适配器
    observability/         # 日志与指标
  management/
    http/                  # 路由、鉴权上下文、DTO
    assets/                # 构建后的静态资源嵌入边界
web/
  src/
    api/                   # 类型化 API client
    features/              # accounts、operations、settings
    components/            # 无业务规则的复用组件
    state/                 # 页面状态与 busy 生命周期
docs/                      # 设计、运维和发布文档
release/                   # registry 模板、校验和与打包描述
```

## 依赖方向

`domain <- application <- adapters`。领域层不依赖其他内部层；应用层只依赖领域和 ports；适配器实现 ports。UI 只依赖管理 API 契约。

## 文件与复杂度约束

- 每个文件围绕一个明确职责，不设共享的“万能 utils”。
- HTTP handler 只做协议转换；用例必须位于 application。
- 前端按 feature 拆分，表格、操作抽屉和设置页不合并为单一巨型页面。
- schema 与 DTO 有独立版本，禁止直接序列化领域对象作为外部契约。
- 通过依赖检查和评审拒绝循环依赖及跨层读写。

## 后果

目录数量增加，但安全规则可集中测试，CPA ABI 变化和 UI 替换不会迫使领域规则重写。

## 被拒绝方案

- 单个服务文件承载 ABI、路由、状态和规则：难以测试与审计。
- UI 直接调用 host API：绕过统一鉴权、保护和审计。
- 以数据库 record 作为所有层共享模型：会把持久化 schema 泄漏到 API 和领域。
