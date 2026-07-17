# cpa-grok-panel

[![Release](https://img.shields.io/github/v/release/magicvr/cpa-grok-panel)](https://github.com/magicvr/cpa-grok-panel/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Platform](https://img.shields.io/badge/platform-Linux%20amd64-blue)](https://github.com/magicvr/cpa-grok-panel/releases)

CLIProxyAPI（CPA）的 **Grok / xAI OAuth 账号运维面板**。

在 CPA 管理页中集中查看账号状态、统计真实 Token 用量，并安全地做启用 / 停用 / 降权 / 删除等操作。v0.3.10 起支持**优先级冷却恢复**：降权后按 `6h → 12h → 24h` 阶梯自动恢复（明确标记为机器人的账号除外）。

插件 id：`cpa-grok-panel`

## 友链

> 感谢 [LINUX DO](https://linux.do/) 社区对开源项目的支持。本项目的开发与推广得益于社区环境，在此致敬。

[![LINUX DO](https://img.shields.io/badge/LINUX%20DO-社区友链-0066cc)](https://linux.do/)

---

## 目录

- [功能概览](#功能概览)
- [安装](#安装)
  - [前置条件](#前置条件)
  - [方式 A：商店源安装（推荐）](#安装方式-a推荐把本仓库-registry-加入-cpa-插件商店)
  - [方式 B：手动安装](#安装方式-b手动下载-github-release-安装)
  - [安装方式对照](#安装方式对照)
- [使用](#使用)
- [设置与环境变量](#设置与环境变量)
- [开发与构建](#开发与构建)
- [相关文档](#相关文档)

## 功能概览

| 能力 | 说明 |
| --- | --- |
| **账号列表** | 读取 CPA 中的 xAI OAuth 账号，展示机器人标记、启停、优先级、请求数与 Token 用量 |
| **用量统计** | 累计 CPA `usage` 回调中的真实 input / output / total token，不做内容估算 |
| **账号操作** | 单账号与批量：启用、停用、降权、解除降权、设置优先级 |
| **自动降权** | 401/403 始终计入连败；429 / 5xx 可选计入；默认连续 3 次后降权 |
| **优先级冷却恢复** | 默认开启；降权后按 6h → 12h → 24h（封顶）恢复并清空失败诊断；再次降权递增冷却 |
| **安全删除** | 删除前重新校验 `auth_index` 与精确文件名映射；成功后清理插件本地状态 |
| **每日清零** | 可按服务器本地时区每天清零请求数、Token 累计与连续失败计数 |
| **持久化设置** | 面板设置与统计状态写入插件 state，重启后继续生效 |

**当前未实现**：主动健康检查、套餐核验、Responses 实测。CPA 目前未向插件提供 `host.auth.invoke`，面板不会伪造这些结果。

## 安装

### 前置条件

- CPA 已启用插件（`plugins.enabled: true`），并支持原生插件 ABI、Management API 与 usage 回调
- 持有有效的 CPA management key
- 运行平台为 **Linux amd64**（当前仅发布该平台包）
- CPA 主机能访问 GitHub（`api.github.com`、`github.com`、`raw.githubusercontent.com` 及 Release 资源域名）。出网不稳时，请在 CPA 配置 `proxy-url` 后再走方式 A

### 安装方式 A（推荐）：把本仓库 registry 加入 CPA 插件商店

向 CPA 注册商店目录源（`store-sources`），让插件商店能**列出**本插件；安装时 CPA 再按 `repository` 从 **GitHub Releases** 拉取 zip。

```text
store-sources  →  registry.json（目录 / 元数据）
plugin.repository  →  GitHub Releases（真正下载 .so）
```

#### 1. 商店目录 URL

```text
https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

| 字段 | 值 |
| --- | --- |
| `id` | `cpa-grok-panel` |
| `name` | Grok 账号面板 |
| `version` | 与最新 Release 对齐（例如 `0.3.10`） |
| `repository` | `https://github.com/magicvr/cpa-grok-panel` |

确认可访问：

```bash
curl -fsSL https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

#### 2. 在 CPA 配置中添加 `store-sources`

编辑 `config.yaml`（路径以你的部署为准），合并如下内容：

```yaml
plugins:
  enabled: true
  # dir: plugins          # 保持现有插件目录即可
  store-sources:
    # 可与其它商店源并存；追加本 URL，勿整段覆盖
    - https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
  configs:
    cpa-grok-panel:
      enabled: true
```

- `store-sources` 为字符串数组，每项是 registry.json 的 HTTPS URL
- 已有其它源时**追加**本 URL
- `configs.cpa-grok-panel.enabled: true` 表示安装后允许启用；若 CPA 安装时会自动写入，也可先只加 `store-sources`
- 修改后按 CPA 版本要求**重载配置或重启**，使商店源生效

> 管理页若提供「插件商店源 / store sources」设置，粘贴同一 URL 即可，效果等价。以你当前 CPA 管理页字段为准。

#### 3. 在插件商店安装

1. 打开 CPA 管理页（例如 `http://<cpa-host>:<port>/management.html`），用 management key 登录
2. 进入 **插件 / 插件商店**
3. 刷新后应看到 **Grok 账号面板** / id **`cpa-grok-panel`**
4. 选择版本（一般选最新，例如 `0.3.10`）并安装
5. 安装成功后 **完整停止并重新启动整个 CPA 进程**  
   本插件是原生 `.so`：仅热更新、只重载配置或不杀进程替换动态库，可能导致仍加载旧库或注册异常

也可用 Management API（需已在商店目录中解析到该插件）：

```http
POST /v0/management/plugin-store/cpa-grok-panel/install
Authorization: Bearer <management-key>
Content-Type: application/json

{"version":"0.3.10"}
```

版本号请与 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) 上已发布 tag 一致（去掉前缀 `v` 的 semver）。

#### 4. 打开面板

- 管理页菜单：**Grok 账号**（侧栏出现时）
- 或直接访问：

  ```text
  /v0/resource/plugins/cpa-grok-panel/panel
  ```

#### 5. 方式 A 常见问题

| 现象 | 常见原因 | 处理 |
| --- | --- | --- |
| 商店搜不到 | 未写入 / 未生效 `store-sources`，或 URL 写错 | 核对 URL 与配置重载；`curl` 验证 registry.json |
| 能列出但安装失败（GitHub 404） | 仓库不可见、Release 不存在、或 CPA 拿不到公开资源 | 确认仓库公开且对应版本已发 Release；检查出网 / 代理 |
| 安装失败（403 / rate limit） | 未认证访问 GitHub API 配额或代理拦截 | 等待重试、配置 `proxy-url`、检查出口 IP |
| 安装成功但 `registered: false` | 动态库未完整加载或 reconfigure 异常 | **完整重启 CPA**；再查 `GET /v0/management/plugins` |
| 面板仍是旧版本 | 热替换 `.so` 或浏览器缓存 | 完整重启 + 浏览器强刷；看面板副标题版本号 |

### 安装方式 B：手动下载 GitHub Release 安装

适合：暂不改 `store-sources`、离线拷包，或商店链路不通。

1. 打开 [GitHub Releases](https://github.com/magicvr/cpa-grok-panel/releases)，下载平台包，例如：
   - `cpa-grok-panel_0.3.10_linux_amd64.zip`
   - （可选）同 Release 下的 `checksums.txt`
2. 在 CPA **插件管理**中本地安装 / 上传该 zip  
   **不要**改压缩包内部结构：包根目录应直接是 `cpa-grok-panel.so`
3. 安装完成后 **完整停止并重新启动 CPA**
4. 打开面板（路径与方式 A 相同）

### 安装方式对照

| | 方式 A（商店源） | 方式 B（手动包） |
| --- | --- | --- |
| 改 CPA 配置 | 是：追加 `store-sources` | 否 |
| 升级 | 商店内选版本 | 每次手动下新 zip |
| 出网 | 需访问 GitHub（目录 + Release） | 可在有网机器下载后拷到 CPA 主机 |
| 安装后 | **完整重启 CPA** | 同左 |

## 使用

### 首次打开

进入面板 **设置** 页，填写 CPA management key 并保存。密钥只写入当前浏览器 `localStorage`，不进插件 state；清理浏览器数据或换浏览器后需重新填写。

### 账号列表

- 按文件名搜索；按启停、是否降权、机器人检测结果筛选
- 分页 20 / 50 / 100；可跳转首页、末页或指定页
- 可按账号文件、状态、机器人、优先级、总 Token、成功 / 失败数排序
- 顶部汇总：账号数、已降权数、成功 / 失败请求数、累计 Token
- 「已降权」判定：`priority <= demotion_priority`

### 单账号操作

| 操作 | 行为 |
| --- | --- |
| **启用 / 停用** | 通过 CPA Management API 修改运行状态 |
| **降权** | 经 fields API 写入降权目标，成功后保存写前优先级作为恢复基线 |
| **解除降权** | 优先恢复已记录基线；无可靠基线时用「默认恢复优先级」 |
| **人工解除降权优待** | 立即恢复并将冷却阶梯重置为 0；机器人账号也可人工解除 |
| **诊断清理** | 启停、手动降权 / 解除降权成功后清空连败、上次失败时间与失败码；自动降权保留诊断 |
| **安全删除** | 须输入精确文件名确认；删除前重新核对映射，映射已变则跳过 |

### 批量操作

- 表头复选框选当前页；「全部选中」选当前筛选结果；「清除选中」取消全部
- 支持批量启用、停用、降权、解除降权、设置优先级、安全删除
- 批量设置优先级要求输入整数，经 fields API 按精确文件名写入
- 有限并发执行，显示进度与成功 / 跳过 / 失败数；默认并发 10，可在设置页改为 1–50
- 批量删除须输入 `DELETE`，且每个账号删除前再次校验映射

### 自动刷新

默认开启，间隔 5 秒；仅在页面可见且无账号操作时执行。设置页可关闭，或将间隔调为 2–60 秒。关闭后账号页右上角显示手动「刷新」。

### 自动降权

- 归因到账号的 HTTP 401/403 **始终**计入连续失败阈值（不再单次立即降权）
- 可计数状态共用阈值，默认 `3`；429 / 5xx 默认不计数，可在设置页分别开启
- 默认降权优先级 `-100`；`priority <= demotion_priority` 即视为已降权
- 优先走可选的 Management fields HTTP 写入，未配置时回退 `host.auth.save`；两种路径都会重读账号列表校验
- 已记录为 `applied` 但宿主优先级仍高于降权目标时，列表读取或 worker 周期会重新请求降权，并标记「记录陈旧 / host 未降权」
- 「优先级冷却恢复」默认开启；自动恢复保留当前冷却阶梯，下次降权继续递增；**机器人账号不会自动恢复**

### 每日清零

默认关闭，默认时间 `00:00`，使用插件进程所在服务器本地时区。启用后清零请求数、Token 累计与连续失败计数。启动时若当天已过设定时间且尚未执行，会补执行一次。

### 诊断列与机器人列

**诊断列**直接显示连续归因失败次数、上次失败码，以及处理中 / 已降权 / 失败标记。悬停可查看上次失败时间、降权状态、目标优先级、恢复基线、触发时间与降权失败码。

**机器人列**只读解析账号 `access_token` 的 JWT payload：

| 显示 | 条件 |
| --- | --- |
| 红色「是」 | `bot_flag_source` 为数字 `1` 或字符串 `"1"` |
| 绿色「否」 | 有效 token 且无标记 |
| 灰色「—」 | 无 token、无效 JWT，或单账号凭据读取失败 |

列表读凭据使用有限并发，**不会**写入插件 state。

## 设置与环境变量

面板保存过设置后，以持久化 state 中的 settings 为准。以下 `CPA_GROK_*` 环境变量**仅在插件首次启动且尚无持久化设置时**作为初始值：

| 环境变量 | 默认值 | 说明 |
| --- | ---: | --- |
| `CPA_GROK_BATCH_CONCURRENCY` | `10` | 浏览器批量操作并发，范围 1–50 |
| `CPA_GROK_FAILURE_THRESHOLD` | `3` | 连续失败阈值，范围 1–100 |
| `CPA_GROK_DEMOTION_PRIORITY` | `-100` | 自动 / 手动降权目标优先级 |
| `CPA_GROK_DEFAULT_RESTORE_PRIORITY` | `0` | 无可靠基线时的恢复优先级 |
| `CPA_GROK_COOLDOWN_RESTORE` | `true` | 是否默认开启优先级冷却恢复 |
| `CPA_GROK_COUNT_429` | `false` | 是否将 429 计入连败阈值 |
| `CPA_GROK_COUNT_5XX` | `false` | 是否将 5xx 计入连败阈值 |
| `CPA_GROK_MANAGEMENT_BASE_URL` | 未设置 | 自动降权 / 冷却恢复使用的 CPA 地址，例如 `http://127.0.0.1:8317`；需与 key 同时设置 |
| `CPA_GROK_MANAGEMENT_KEY` | 未设置 | 调用 Management fields API 的 key；未成对设置时回退 `host.auth.save` |

自动刷新与每日清零可在设置页直接改，无需重启。Management 地址与 key 由插件进程环境读取，变更后需重启。

## 开发与构建

插件使用 Go + CGO，以 `c-shared` 模式构建原生动态库。

```bash
# Linux amd64
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -buildmode=c-shared -o cpa-grok-panel.so .

# 测试
go test ./...
```

## 相关文档

- 架构、CPA 集成、接口与持久化：[docs/design/](docs/design/)
- 能力探测与评审记录：[docs/reviews/](docs/reviews/)
- 发行版与变更：[Releases](https://github.com/magicvr/cpa-grok-panel/releases)

README 以当前 **v0.3.10** 可安装版本为准。
