# cpa-grok-panel

## 友链

> 感谢 [LINUX DO](https://linux.do/) 社区对开源项目的支持。本项目的开发与推广得益于社区环境，在此致敬。

[![LINUX DO](https://img.shields.io/badge/LINUX%20DO-社区友链-0066cc)](https://linux.do/)

---

**CPA（CLIProxyAPI）原生插件：面向 xAI / Grok OAuth 账号的列表运维面板。**

| | |
| --- | --- |
| **适合** | 在 CPA 上维护大量 xAI OAuth 账号：看启停与优先级、累计真实 usage、失败后可控降权与恢复、批量改优先级 / 安全删除 |
| **不适合 / 未做** | 主动健康检查、套餐核验、Responses 实测（CPA 未向插件提供 `host.auth.invoke`；面板**不会伪造**这类结果） |
| **插件 id** | `cpa-grok-panel` |
| **发布平台** | 当前仅 **Linux amd64** Release（`.so` 动态库） |
| **当前版本** | 以 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) / `registry.json` 为准（文档与代码按 v0.3.10 叙述） |

---

## 功能概览

- **账号列表**：投影 CPA 中的 xAI OAuth 账号；支持搜索、筛选、分页、排序；展示机器人标记、启停、优先级、请求数与 Token。
- **用量统计**：累计 CPA `usage` 回调中的真实 input / output / total token，不根据请求内容估算。
- **账号操作**：单账号 / 批量启用、停用、降权、解除降权；**批量设置优先级**（Management `fields`）；安全删除（映射二次校验）。
- **自动降权**：`401` / `403` **始终**计入连续失败；`429` / `5xx` 可在设置中开关。所有可计数状态**共用同一阈值**，默认连续 **3** 次后请求降权（目标优先级默认 `-100`）。
- **冷却恢复**：默认开启；降权后按 **6h → 12h → 24h**（封顶 24h）自动恢复优先级并清空失败诊断；再次降权递增冷却。明确标记为机器人的账号**不**自动恢复（仍可人工解除降权）。
- **诊断与陈旧态**：诊断列展示连败、失败码、降权状态；`applied` 但 host 优先级已回漂时，会重新入队，并提示「记录陈旧/host未降权」。
- **每日清零**（可选）：按服务器本地时区清零请求数、Token 累计与连续失败。
- **设置持久化**：面板设置与统计写入插件 state，重启后保留。

---

## 安装

### 前置条件

- CPA 已启用插件（`plugins.enabled: true`），并支持原生插件 ABI、Management API、usage 回调。
- 持有 CPA **management key**。
- 运行平台 **Linux amd64**。
- CPA 主机能访问 **GitHub**（`api.github.com`、`github.com`、`raw.githubusercontent.com`、Release 资源）。不稳定时在 CPA 配置 `proxy-url` 后再走方式 A。

### 方式 A（推荐）：商店源 `store-sources`

```text
store-sources → registry.json（目录/元数据）
plugin.repository → GitHub Releases（下载 .so 安装包）
```

**1. 目录 URL**

```text
https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

| 字段 | 值 |
| --- | --- |
| `id` | `cpa-grok-panel` |
| `name` | Grok 账号面板 |
| `version` | 与最新 Release 对齐（如 `0.3.10`） |
| `repository` | `https://github.com/magicvr/cpa-grok-panel` |

```bash
curl -fsSL https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

**2. 写入 CPA `config.yaml`**

```yaml
plugins:
  enabled: true
  # dir: plugins
  store-sources:
    # 与其它源并存；追加本行即可
    - https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
  configs:
    cpa-grok-panel:
      enabled: true
```

- `store-sources` 为字符串数组；**追加**本 URL，勿覆盖掉已有源。
- 改配置后按 CPA 要求**重载或重启**，使商店源生效。
- 若管理页有「插件商店源」类设置，粘贴同一 URL 即可。

**3. 商店安装**

1. 打开 CPA 管理页（如 `http://<host>:<port>/management.html`），用 management key 登录。
2. 进入插件商店，刷新后找到 **Grok 账号面板** / `cpa-grok-panel`。
3. 安装目标版本后，**完整停止并重新启动整个 CPA 进程**（原生 `.so` 不宜只热替换）。

Management API 示例：

```http
POST /v0/management/plugin-store/cpa-grok-panel/install
Authorization: Bearer ***
Content-Type: application/json

{"version":"0.3.10"}
```

版本号与 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) 的 tag 去掉 `v` 后一致。

**4. 打开面板**

- 管理页侧栏：**Grok 账号**
- 或：`/v0/resource/plugins/cpa-grok-panel/panel`

### 方式 B：手动 Release zip

适合不改 `store-sources`、离线拷包或商店链路不通时。

1. 从 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) 下载如 `cpa-grok-panel_0.3.10_linux_amd64.zip`（可选 `checksums.txt`）。
2. 在 CPA 插件管理中上传安装。zip **根目录**即为 `cpa-grok-panel.so`，不要多套一层目录。
3. **完整重启 CPA**，再用与方式 A 相同路径打开面板。

### 方式对照

| | A 商店源 | B 手动包 |
| --- | --- | --- |
| 改配置 | 追加 `store-sources` | 否 |
| 升级 | 商店选版本 | 每次下新 zip |
| 出网 | 需访问 GitHub | 可他机下载后拷贝 |
| 安装后 | **均需完整重启 CPA** | 同左 |

### 安装常见问题

| 现象 | 处理 |
| --- | --- |
| 商店搜不到 | 检查 `store-sources`、配置是否生效；`curl` 验证 registry |
| 列出但安装失败（GitHub 404/403） | 确认 Release 存在；配置 `proxy-url`；检查限流 |
| `registered: false` / 面板仍旧版 | **完整重启 CPA** + 浏览器强刷；看面板版本号 |
| 装 0.3.8 后 CPA panic | 已在 **0.3.9+** 修复（typed-nil priority writer）；请用 **≥ 0.3.9**，推荐最新 0.3.10 |

---

## 使用

### 首次打开

在 **设置** 页填写 management key 并保存。密钥只在当前浏览器 `localStorage`，不写入插件 state；清站点数据或换浏览器需重填。

### 账号列表

- 搜索文件名；按启停、是否降权、机器人结果筛选。
- 分页 20/50/100；可按文件名、状态、机器人、优先级、Token、成功/失败排序。
- 顶部汇总：账号数、已降权数、成功/失败请求、累计 Token。
- 「已降权」判定：`priority <= demotion_priority`（默认 `-100`）。

### 单账号 vs 批量

| 能力 | 单行 | 批量 |
| --- | --- | --- |
| 启用 / 停用 | ✓ | ✓ |
| 降权 / 解除降权 | ✓（完整账号字段；写 Management `fields` 后确认插件 state） | ✓ |
| 设置优先级 | — | ✓（输入整数，`fields` 写入） |
| 安全删除 | ✓（输入精确文件名） | ✓（输入 `DELETE`，逐条校验映射） |

- **降权**：写入降权目标后保存写前优先级为恢复基线。
- **解除降权**：优先恢复记录的基线；无基线时用「默认恢复优先级」。人工解除会重置冷却阶梯；机器人也可人工解除。
- **诊断清理**：启停、**手动**降权/解除成功后清空连败与失败码；**自动**降权保留诊断。
- 批量默认并发 10（设置中 1–50），有进度与成功/跳过/失败统计。

### 自动降权规则（简表）

| 状态码 | 默认是否计入阈值 | 说明 |
| --- | --- | --- |
| 401 / 403 | **是**（始终） | 走连续失败，**不是**单次立刻降权 |
| 429 / 5xx | 否 | 可在设置中分别打开 |
| 阈值 | 默认 **3** | 所有可计数状态共用 |

写入路径：优先可选环境变量里的 Management `fields`；未配置则 `host.auth.save`。写后会 re-list 校验。

冷却：6h → 12h → 24h；自动恢复保留阶梯；机器人跳过自动恢复。

### 每日清零

默认关闭，时间默认 `00:00`（服务器本地时区）。清零请求数、Token、连败；启动时若当天已过设定时间且未跑过会补一次。

### 诊断列与机器人列

- **诊断**：连败次数、上次失败码；hover 可见失败时间、降权状态、目标/基线、触发时间等。
- **机器人**：只读解析 `access_token` JWT 的 `bot_flag_source`（`1` / `"1"` → 是）；无 token 或无效 JWT → `—`。不写入插件 state。

---

## 设置与环境变量

面板**保存过设置后**，以 state 中的 settings 为准。下表 `CPA_GROK_*` **仅在首次启动且尚无持久化设置时**作为初值：

| 环境变量 | 默认 | 说明 |
| --- | ---: | --- |
| `CPA_GROK_BATCH_CONCURRENCY` | `10` | 批量并发 1–50 |
| `CPA_GROK_FAILURE_THRESHOLD` | `3` | 连续失败阈值 1–100 |
| `CPA_GROK_DEMOTION_PRIORITY` | `-100` | 降权目标优先级 |
| `CPA_GROK_DEFAULT_RESTORE_PRIORITY` | `0` | 无基线时的恢复优先级 |
| `CPA_GROK_COOLDOWN_RESTORE` | `true` | 默认是否开启冷却恢复 |
| `CPA_GROK_COUNT_429` | `false` | 429 是否计入阈值 |
| `CPA_GROK_COUNT_5XX` | `false` | 5xx 是否计入阈值 |
| `CPA_GROK_MANAGEMENT_BASE_URL` | 空 | 自动降权/冷却走 Management 的 CPA 地址（如 `http://127.0.0.1:8317`） |
| `CPA_GROK_MANAGEMENT_KEY` | 空 | 与上一项成对；未成对则自动写路径回退 `host.auth.save` |

自动刷新、每日清零等可在设置页改，一般**不必**重启插件。  
`CPA_GROK_MANAGEMENT_*` 由插件进程环境读取，变更后需**重启 CPA**。

`/meta` 会报告 `state_status`：`healthy` / `memory` / `degraded`（以及 backend、data_dir 等），便于判断 state 是否落盘。

---

## 故障排查

| 现象 | 建议 |
| --- | --- |
| 装不上 / 商店没有 | 核对 `store-sources`、registry URL、GitHub 出网与 Release |
| 安装成功但行为像旧版 | **完整重启 CPA**；强刷浏览器；看面板版本 |
| 0.3.8 安装后 CPA 崩溃 | 升级到 **≥ 0.3.9**（nil priority writer）；推荐 0.3.10 |
| 诊断显示已降权，优先级列仍是 0 | 看是否「记录陈旧/host未降权」；确认 Management key 可用；自动路径可配 `CPA_GROK_MANAGEMENT_*`；列表会 reconcile |
| meta `state_status=memory/degraded` | 数据目录权限/磁盘；memory 时重启会丢本地统计与设置意图 |
| 面板读不了账号 | 设置页 management key 是否正确、是否同浏览器 |

---

## 开发与构建

Go + CGO，`c-shared` 产出原生动态库：

```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -buildmode=c-shared -o cpa-grok-panel.so .
```

```bash
CGO_ENABLED=1 go test ./...
```

设计资料见 [docs/design/](docs/design/)。**以可安装 Release / 本 README 为准**；设计文档可能滞后。
