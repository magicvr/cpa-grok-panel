# 08. 测试与发布

## 1. 测试分层

| 层 | 覆盖 |
| --- | --- |
| 领域单测 | usage 累计、失败阈值、降权/恢复、保护规则、设置不变量 |
| 适配层契约测 | usage DTO 归一化、host 错误映射、能力缺失降级 |
| 应用服务测 | operation 生命周期、幂等键、部分失败、确认令牌 |
| 持久化测 | 原子写、迁移、损坏恢复、锁 |
| HTTP 测 | 鉴权、校验、状态码、不泄漏秘密 |
| UI 测 | busy/generation、清除选中、禁用态 |
| 集成/契约 | 真实或模拟 CPA ABI 矩阵 |
| 手工验收 | 灰度清单、删除双确认、回滚 |

## 2. 关键负面用例

- 无可信 `auth_index` 的失败 usage → 不降权
- exact 模式重复 `event_id` → 不双计；weak 模式 → 明确允许漏重/误判边界
- 仅 `auth_index`、缺少精确 `.json` name 的 Management 写 → 拒绝
- usage handler 达阈值 → 只入队，不同步调用 Management PATCH
- 无 revision → 串行写 + 写后 re-list；不得宣告 CAS
- 外部已改 priority → 恢复返回 `priority_superseded`
- 确认令牌过期/复用 → 删除拒绝
- write_mode=read_only → 所有写失败且原因明确
- capability 缺失 → 功能禁用而非崩溃

## 3. 发布产物（Linux amd64 优先）

```text
cpa-grok-panel_<version>_linux_amd64.zip
  cpa-grok-panel.so   # 或 CPA 要求的命名
checksums.txt         # sha256
registry.json         # 商店源
```

命名与校验格式以 CPA 商店规范最终确认为准。

## 4. 版本与兼容

- SemVer：破坏性 API/ABI 升主版本
- registry 声明 `cpa_version_range` 与 platform
- 发布说明包含 capability 依赖与降级行为

## 5. 灰度与回滚

1. 安装 `cpa-grok-panel`，`write_mode=read_only`
2. 对照列表与 usage 累计
3. 停旧插件写责任 → 新插件 `managed`
4. 小流量失败降权验证
5. 回滚：新插件只读 → 恢复旧插件写

## 6. CI 最低门槛

- `go test ./...` / 前端 lint+unit
- 禁止密钥模式扫描
- 构建可复现（trimpath / 锁定依赖）

## 7. 非目标

- 不在 MVP 做全平台矩阵（arm64/mac/win 后续）
- 不依赖生产 CPA 做唯一测试环境
