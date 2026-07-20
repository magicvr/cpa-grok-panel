# cpa-grok-panel

[![Release](https://img.shields.io/github/v/release/magicvr/cpa-grok-panel)](https://github.com/magicvr/cpa-grok-panel/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Platform](https://img.shields.io/badge/platform-Linux%20amd64-blue)](https://github.com/magicvr/cpa-grok-panel/releases)

**CLIProxyAPI（CPA）** 的 Grok / xAI OAuth 账号运维面板。

在 CPA 管理页集中查看账号状态、Token 用量与套餐缓存，并安全地启用 / 停用 / 降权 / 删除账号。  
插件 id：`cpa-grok-panel` · 当前文档对应 **v0.5.0**（Linux amd64）。

## 友链

> 感谢 [LINUX DO](https://linux.do/) 社区对开源项目的支持。

[![LINUX DO](https://img.shields.io/badge/LINUX%20DO-社区友链-0066cc)](https://linux.do/)

---

## 目录

- [功能概览](#功能概览)
- [安装](#安装)
- [使用](#使用)
- [设置与环境变量](#设置与环境变量)
- [开发与发版](#开发与发版)
- [相关文档](#相关文档)

## 功能概览

| 能力 | 说明 |
| --- | --- |
| **账号列表** | 展示 xAI OAuth 账号的套餐、启停、机器人标记、优先级、请求数与用量 |
| **套餐（手动）** | 管理员手动刷新；默认 `unknown`；失败记 `unknown`；成功且非 SuperGrok / SuperGrok Heavy → `Free`；结果持久缓存，仅下次手动刷新覆盖 |
| **用量列** | 展示 `用量/限额` + 进度条；付费且有官方限额时用 billing；Free / 无线额时用本插件日 token 与 Free 日限额（默认 2M） |
| **用量统计** | 累计 CPA `usage` 回调中的真实 input / output / total token |
| **账号操作** | 单账号与批量：启用、停用、降权、解除降权、设置优先级 |
| **Soft / hard 自动降权** | failure debt 与 hard streak 双轨；默认 debt 2.0 进入 soft `-10`，连败 3 次或 debt 4.5 进入 hard `-100` |
| **Half-open 冷却恢复** | soft/hard 经 `6h → 12h → 24h` 冷却后先进入 `-10` 观察档；默认累计成功 2 次回 baseline，归因失败立即回 hard |
| **安全删除** | 删除前校验 `auth_index` 与精确文件名；成功后清理插件本地 state |
| **每日清零** | 可按服务器本地时区每天清零请求数、Token 累计与连败计数 |
| **持久化** | 设置、统计、套餐缓存写入插件 state，重启后保留 |

**不做的事**

- 不自动轮询套餐 / 额度（避免打爆上游）
- 不伪造健康检查、Responses 实测结果
- Free 日限额是运维估算分母，不是 xAI 官方账本

## 安装

### 前置条件

- CPA 已启用插件（`plugins.enabled: true`），支持原生插件 ABI、Management API、usage 回调
- 有效的 CPA management key
- 平台：**Linux amd64**
- 方式 A 需要 CPA 主机能访问 GitHub（`api.github.com`、`github.com`、`raw.githubusercontent.com`、Release 资源域名）。出网不稳时配置 `proxy-url`

### 安装方式 A（推荐）：插件商店

商店目录用 `registry.json` 列出插件；安装时 CPA 从 GitHub Releases 下载 **zip 资产**（不是裸 `.so`）。

```text
store-sources  →  registry.json（目录 / 元数据）
plugin.repository  →  GitHub Releases 资产
  cpa-grok-panel_<version>_linux_amd64.zip   ← 商店必需
  （zip 根目录内为 cpa-grok-panel.so）
```

#### 1. 商店源 URL

```text
https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

| 字段 | 值 |
| --- | --- |
| `id` | `cpa-grok-panel` |
| `name` | Grok 账号面板 |
| `version` | 与最新 Release 对齐（如 `0.5.0`） |
| `repository` | `https://github.com/magicvr/cpa-grok-panel` |

```bash
curl -fsSL https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

#### 2. 写入 CPA `store-sources`

编辑 `config.yaml`（路径以你的部署为准），**追加**源，不要整段覆盖其它源：

```yaml
plugins:
  enabled: true
  store-sources:
    - https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
  configs:
    cpa-grok-panel:
      enabled: true
```

- `store-sources`：registry.json 的 HTTPS URL 列表
- 管理页若有「插件商店源」输入框，粘贴同一 URL 即可
- 改完后按 CPA 要求**重载配置或重启**，商店源才生效

#### 3. 安装

1. 打开 CPA 管理页（如 `http://<cpa-host>:<port>/management.html`），用 management key 登录  
2. **插件 / 插件商店** → 找到 **Grok 账号面板**（id `cpa-grok-panel`）  
3. 选择版本（一般最新，如 `0.5.0`）并安装
4. **完整停止并重新启动整个 CPA 进程**（原生 `.so`：热更新 / 只重载配置可能仍加载旧库）

Management API 示例：

```http
POST /v0/management/plugin-store/cpa-grok-panel/install
Authorization: Bearer <management_key>
Content-Type: application/json

{"version":"0.5.0"}
```

版本号为去掉 `v` 前缀的 semver，须与 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) 已发布 tag 一致。

#### 4. 打开面板

- 侧栏菜单：**Grok 账号**
- 或：`/v0/resource/plugins/cpa-grok-panel/panel`

### 安装方式 B：手动上传 Release zip

适合不改 `store-sources`、离线拷包或商店链路不通。

1. 在 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) 下载  
   - **`cpa-grok-panel_0.5.0_linux_amd64.zip`**（商店与手动安装都用这个名字）
   - （可选）`checksums.txt`  
2. CPA **插件管理**里本地安装 / 上传该 zip  
   - zip **根目录**必须是 `cpa-grok-panel.so`，不要改包内结构  
3. **完整重启 CPA**  
4. 打开面板（路径同方式 A）

### 安装方式对照

| | 方式 A（商店） | 方式 B（手动 zip） |
| --- | --- | --- |
| 改配置 | 追加 `store-sources` | 否 |
| 升级 | 商店选版本 | 每次下新 zip |
| 出网 | 需访问 GitHub | 可先下载再拷到 CPA 主机 |
| 装完后 | **完整重启 CPA** | 同左 |

### 安装常见问题

| 现象 | 常见原因 | 处理 |
| --- | --- | --- |
| 商店搜不到 | `store-sources` 未写 / 未生效 | 核对 URL，重载或重启；`curl` 测 registry |
| `plugin_install_failed` 且 message 含 **`..._linux_amd64.zip not found`** | Release 只传了裸 `.so`，缺商店约定 zip 名 | 使用带 zip 的 Release；维护者见下方 [发版打包](#发版打包) |
| 安装失败 GitHub 404 | 无对应 tag/Release 或仓库不可见 | 确认公开仓库与版本已发 Release |
| 403 / rate limit | GitHub API 配额或代理拦截 | 配 `proxy-url`、换出口或稍后重试 |
| `registered: false` / 菜单没有 | `.so` 未完整加载 | **完整重启 CPA**；查 `GET /v0/management/plugins` |
| 面板版本旧 | 热替换或浏览器缓存 | 完整重启 + 强刷；看面板副标题版本号 |

## 使用

### 首次打开

进入 **设置**，填写 CPA management key 并保存。密钥只在当前浏览器 `localStorage`，不写插件 state；清站点数据或换浏览器需重填。

### 账号列表

- 搜索文件名；按启停、是否降权、机器人结果筛选  
- 分页 20 / 50 / 100  
- 可排序：账号文件、**套餐**、状态、机器人、优先级、**用量**、成功 / 失败数  
- 顶部汇总：账号数、已降权、成功 / 失败请求、累计 Token  
- 「已降权」：宿主 priority 处于 hard 档，或插件记录为已应用的 `soft / hard / half_open` class

### 套餐与用量

| 项 | 行为 |
| --- | --- |
| **套餐列** | 显示缓存类型：`unknown` / `Free` / `SuperGrok` / `SuperGrok Heavy` |
| **刷新套餐** | 行内「刷新套餐」或批量「批量刷新套餐」；**仅管理员手动**，列表自动刷新不会拉 billing |
| **刷新成功** | SuperGrok / SuperGrok Heavy 按证据映射；其余成功结果记为 **Free** |
| **刷新失败** | 记为 **unknown**（并保留错误信息供悬停查看） |
| **缓存** | 写入插件 state；**直到下次手动刷新**才覆盖，列表轮询不会改写套餐类型 |
| **用量列** | 上：`用量/限额`；下：进度条。Free 或无官方限额时：用量 = 本插件日 token，限额 = 设置「Free 用户日限额」（默认 2M） |

技术路径：面板用 management key 调用 CPA `POST /v0/management/api-call`，以指定 `authIndex` 请求 Grok billing（与 CPA 自带配额页同源能力），再 `POST` 插件 `/accounts/quota` 落盘。

### 单账号操作

| 操作 | 行为 |
| --- | --- |
| **启用 / 停用** | Management status API |
| **降权** | fields 写目标优先级，成功后保存写前优先级为基线 |
| **解除降权** | 优先恢复基线；无基线用「默认恢复优先级」 |
| **刷新套餐** | 见上表；不自动执行 |
| **诊断清理** | 启停、手动降权 / 解除成功后清空 streak、debt 与失败码；自动降权保留诊断 |
| **安全删除** | 须输入精确文件名；映射变化则跳过 |

### 批量操作

- 表头选当前页；「全部选中」= 当前筛选结果；「清除选中」取消全部  
- 支持：启用、停用、降权、解除降权、设置优先级、**刷新套餐**、安全删除  
- 批量设置优先级：输入整数，经 fields API 按精确文件名写入  
- 有限并发（默认 10，设置页 1–50）；套餐刷新并发更保守（约 3）  
- 批量删除须输入 `DELETE`，且每项删除前再校验映射  

### 自动刷新（列表）

默认开启、间隔 5 秒；仅页面可见且无账号操作时执行。**只刷新列表 / 设置 / meta，不刷新套餐。**  
可在设置页关闭或改为 2–60 秒；关闭后账号页显示手动「刷新」。

### 自动降权与冷却

- 401/403 每次默认增加 `1.5` failure debt；成功默认衰减 `1.0`，不会直接清空历史；非归因失败不改 debt
- debt 默认达到 `2.0` 进入 soft `-10`；hard streak 达 3 次或 debt 达 `4.5` 进入 hard `-100`
- 429 可选计入 streak，并在开启时默认增加 `0.5` debt；5xx 可选计入 streak
- priority 写入由 worker 异步执行，优先 Management fields，未配 `CPA_GROK_MANAGEMENT_*` 时回退 `host.auth.save`，写后 re-list 校验
- 冷却恢复默认开：`6h → 12h → 24h` 后进入 half-open `-10`；默认成功 2 次回 baseline，归因失败立即回 hard；机器人账号不自动恢复

### 每日清零

默认关，默认 `00:00`（插件进程本地时区）。清零请求数、Token 累计与 hard streak；failure debt 保留。启动时若当天已过点且未执行，会补一次。

### 诊断列与机器人列

**诊断**：直接展示 failure debt、hard streak 与 `none / soft / hard / half_open` class；悬停可看证据时间、目标、基线、冷却与 half-open 成功数。

**机器人**（只读解析 `access_token` JWT payload，不写 state）：

| 显示 | 条件 |
| --- | --- |
| 红「是」 | `bot_flag_source` 为 `1` / `"1"` |
| 绿「否」 | 有效 token 且无标记 |
| 灰「—」 | 无 token、无效 JWT 或读失败 |

## 设置与环境变量

面板保存过设置后，以 state 中的 settings 为准。下列环境变量**仅在首次启动且尚无持久化设置时**作初始值：

| 环境变量 | 默认 | 说明 |
| --- | ---: | --- |
| `CPA_GROK_BATCH_CONCURRENCY` | `10` | 批量操作并发 1–50 |
| `CPA_GROK_FAILURE_THRESHOLD` | `3` | 连败阈值 1–100 |
| `CPA_GROK_DEMOTION_PRIORITY` | `-100` | hard 降权目标优先级 |
| `CPA_GROK_SOFT_DEMOTION` | `true` | 是否启用 soft 降权 |
| `CPA_GROK_SOFT_DEMOTION_PRIORITY` | `-10` | soft / half-open 观察档优先级 |
| `CPA_GROK_SOFT_DEBT_THRESHOLD` | `2.0` | soft debt 阈值 |
| `CPA_GROK_HARD_DEBT_THRESHOLD` | `4.5` | hard debt 阈值 |
| `CPA_GROK_DEBT_FAIL_401` | `1.5` | 401/403 debt 加分 |
| `CPA_GROK_DEBT_FAIL_429` | `0.5` | 开启 429 计数时的 debt 加分 |
| `CPA_GROK_DEBT_SUCCESS_DECAY` | `1.0` | 成功请求 debt 衰减 |
| `CPA_GROK_DEFAULT_RESTORE_PRIORITY` | `0` | 无基线时的恢复优先级 |
| `CPA_GROK_COOLDOWN_RESTORE` | `true` | 是否默认开冷却恢复 |
| `CPA_GROK_HALF_OPEN` | `true` | 冷却后是否进入 half-open 观察档 |
| `CPA_GROK_HALF_OPEN_SUCCESS_THRESHOLD` | `2` | half-open 回 baseline 所需成功数 |
| `CPA_GROK_COUNT_429` | `false` | 429 是否计入连败 |
| `CPA_GROK_COUNT_5XX` | `false` | 5xx 是否计入连败 |
| `CPA_GROK_MANAGEMENT_BASE_URL` | 未设置 | 自动降权 / 冷却恢复用的 CPA 地址（如 `http://127.0.0.1:8317`） |
| `CPA_GROK_MANAGEMENT_KEY` | 未设置 | Management fields 用 key；须与 BASE 成对，否则回退 `host.auth.save` |

设置页还可改：自动刷新、每日清零、**Free 用户日限额（token，默认 2000000）** 等，热生效、无需重启。  
`CPA_GROK_MANAGEMENT_*` 变更后需重启插件进程。

## 开发与发版

### 构建与测试

```bash
# Linux amd64 动态库
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o dist/cpa-grok-panel.so .

CGO_ENABLED=1 go test ./...
go vet ./...
```

### 发版打包

CPA 插件商店**只认**下列资产名（与历史 0.3.x 一致）：

```text
cpa-grok-panel_<semver>_linux_amd64.zip   # zip 根目录：cpa-grok-panel.so
checksums.txt                             # 建议一并上传
```

**不要只上传裸 `cpa-grok-panel.so`**，否则商店会报：

```text
plugin_install_failed: release asset cpa-grok-panel_<ver>_linux_amd64.zip not found
```

一键打包：

```bash
./scripts/package_release.sh 0.5.0
# 生成：
#   dist/cpa-grok-panel_0.5.0_linux_amd64.zip
#   dist/checksums.txt
#   dist/cpa-grok-panel.so

gh release upload v0.5.0 \
  dist/cpa-grok-panel_0.5.0_linux_amd64.zip \
  dist/checksums.txt \
  --clobber
```

## 相关文档

- 架构与集成：[docs/design/](docs/design/)
- 评审与探测：[docs/reviews/](docs/reviews/)
- 发行版：[Releases](https://github.com/magicvr/cpa-grok-panel/releases)

README 以当前可安装版本 **v0.5.0** 为准。
