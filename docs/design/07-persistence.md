# 07. 持久化

## 1. 原则

- CPA auth 元数据是真相源；插件 state 只存派生数据
- 单 CPA 实例、单插件 data dir；文件锁防多实例
- 原子写（tmp + fsync + rename）
- schema 版本化，可迁移
- 永不写入 OAuth token、管理密钥、完整 Responses

## 2. 位置

```text
{plugin_data_dir}/
  state.json          # 当前快照（或分片后的 manifest）
  state.json.bak      # 上一成功快照
  locks/instance.lock
  audit/              # 可选滚动审计
```

`plugin_data_dir` 必须由 CPA 分配；禁止写全局任意路径。

## 3. 快照 schema（概念）

```json
{
  "schema_version": 1,
  "plugin_id": "cpa-grok-panel",
  "plugin_version": "0.1.0",
  "saved_at": "2026-07-15T12:00:00Z",
  "settings": {},
  "statistics_started_at": "2026-07-15T00:00:00Z",
  "accounts": {
    "<auth_file_id>": {
      "usage": {},
      "failure": {},
      "demotion": {},
      "health": {},
      "tier": {},
      "last_seen_at": "..."
    }
  },
  "event_dedupe": {
    "window": "ring-or-bloom-policy",
    "recent_ids": []
  },
  "operations": [],
  "audit_cursor": 0
}
```

MVP 可用单文件；账号很多时再拆 `accounts/*.json` + manifest。

## 4. 写入策略

- 内存为工作集；变更进入有界 write-ahead / 串行 writer
- debounce 短窗口合并高频 usage 写（需保证进程崩溃窗口可接受）
- 关键写（降权成功、删除确认、设置更新）同步刷盘
- writer 失败：告警 + fail closed 对危险写；usage 可返回可重试

## 5. 去重

- 以 `event_id` 去重
- 保留最近 N 个或 TTL 窗口
- 与 usage 累计同一提交，避免“计了数但去重丢失”或相反

## 6. 迁移

| 从 → 到 | 策略 |
| --- | --- |
| 无文件 | 创建空 schema_version=1 |
| v1 → v2 | 显式迁移函数；失败则保留旧文件并拒绝启动写 |
| 损坏 | 尝试 `.bak`；仍失败则只读诊断模式或拒绝 register |

不自动导入旧 `grok-panel` state。

## 7. 并发与崩溃

- 启动时取排他锁
- 写中崩溃：rename 原子性保证读者看到完整旧/新文件
- 半写 tmp 可清理

## 8. 保留与隐私

- operations / audit 有上限或滚动
- 文件权限 0600
- 日志与 state 均不落秘密

## 9. 开放问题

- CPA 是否保证专用 data dir 与权限
- usage 峰值下可接受的丢盘窗口
- event_id 全局唯一还是仅进程内
