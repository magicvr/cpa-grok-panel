# cpa-grok-panel

[![Release](https://img.shields.io/github/v/release/magicvr/cpa-grok-panel)](https://github.com/magicvr/cpa-grok-panel/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20Windows-blue)](https://github.com/magicvr/cpa-grok-panel/releases)

**CLIProxyAPI（CPA）** 的 Grok / xAI OAuth 账号运维面板。

在 CPA 管理页集中查看账号状态、Token 用量与套餐缓存，并安全地启用 / 停用 / 降权 / 删除账号。  
插件 id：`cpa-grok-panel` · 当前文档对应 **v0.6.0**（Linux **amd64 / arm64** · Windows **amd64 / arm64**）。

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
| **账号列表** | 展示 xAI OAuth 账号的套餐、启停、风控标记、优先级、请求数与用量 |
| **批量测活** | 面板经 CPA `POST /v0/management/api-call`（`data` 字符串 body）对 `…/v1/responses` 发极短 grok-4.5 请求；结果 `POST /accounts/apply-probe`（`source=manual`）；**不**顺带刷套餐 |
| **自动测活** | Go 侧 `host.auth.get` 取 token + 可选 proxy，POST 同一 endpoint；debt 阈值 / 定时复测触发（`source=auto`） |
| **批量刷新套餐** | 独立按钮；原 billing 逻辑；**不**清掉测活结果 |
| **套餐列** | badge：`Unknown`（plan=unknown）/ Free / SuperGrok / SuperGrok Heavy；失败记 unknown |
| **存活列** | Live(绿) / Exceed(黄) / Dead(红) / Cooling(黄) / Error(黄) / Unknown(灰) |
| **降权档** | `none` 正常 · `watch` 观察 · `anomaly` 异常 · `dead` 死号（替代 soft/hard/half_open） |
| **用量列** | `用量/限额` + 进度条；付费用 billing；Free 用插件日 token 与 Free 日限额（默认 2M） |
| **用量统计** | 累计 CPA `usage` 回调真实 input / output / total token |
| **请求数 host 补偿** | `host.auth.list` success/failed 相对周期 baseline 的增量补偿；与每日清零兼容 |
| **账号操作** | 单账号与批量：启用、停用、降权、解除降权、设置优先级 |
| **批量重签（refresh_token）** | auth 文件 `refresh_token` 换票写回；**不含** SSO；重签不自动解除降权；出站用 `outbound_proxy_url` / `CPA_GROK_OUTBOUND_PROXY`（≠ CPA `proxy-url`） |
| **积分→测活→改档** | 仅正常档（`none`）吃 debt；≥`debt_probe_threshold` 清零 debt 并自动测活；死号冻结积分；观察/异常不因积分触发 |
| **定时复测** | 观察默认 30 分钟、异常默认 6 小时；watch 复测仍 live → 恢复正常档 |
| **外观主题** | 设置页：跟随 CPA / 暗色 / 亮色 |
| **安全删除** | 删除前校验 `auth_index` 与精确文件名；成功后清理插件 state |
| **每日清零** | 可按本地时区清零请求数、Token 累计与连败 streak（debt 保留） |
| **持久化** | 设置、统计、套餐/测活缓存写入插件 state |

**不做的事**

- 不替代 CPA 请求路由 / 负载均衡（只通过 priority 旋钮影响选路）
- 不自动轮询套餐 / 额度（避免打爆上游）
- 不伪造健康检查、Responses 实测结果
- Free 日限额是运维估算分母，不是 xAI 官方账本

## 安装

### 前置条件

- CPA 已启用插件（`plugins.enabled: true`），支持原生插件 ABI、Management API、usage 回调
- 有效的 CPA management key
- 平台：**Linux amd64 / arm64**；**Windows amd64 / arm64**（商店按 CPA 本机 GOOS/GOARCH 选 `…_{goos}_{goarch}.zip`）
- 方式 A 需要 CPA 主机能访问 GitHub（`api.github.com`、`github.com`、`raw.githubusercontent.com`、Release 资源域名）。出网不稳时配置 `proxy-url`

### 安装方式 A（推荐）：插件商店

商店目录用 `registry.json` 列出插件；安装时 CPA 从 GitHub Releases 下载 **zip 资产**（不是裸 `.so`）。

```text
store-sources  →  registry.json（目录 / 元数据）
plugin.repository  →  GitHub Releases 资产
  cpa-grok-panel_<version>_linux_amd64.zip
  cpa-grok-panel_<version>_linux_arm64.zip
  cpa-grok-panel_<version>_windows_amd64.zip  ← Windows x64（zip 内 .dll）
  cpa-grok-panel_<version>_windows_arm64.zip  ← Windows ARM64
  （zip 根目录内均为 cpa-grok-panel.so）
```

#### 1. 商店源 URL

```text
https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

| 字段 | 值 |
| --- | --- |
| `id` | `cpa-grok-panel` |
| `name` | Grok 账号面板 |
| `version` | 与最新 Release 对齐（如 `0.6.0`） |
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
3. 选择版本（一般最新，如 `0.6.0`）并安装
4. **完整停止并重新启动整个 CPA 进程**（原生 `.so`：热更新 / 只重载配置可能仍加载旧库）

Management API 示例：

```http
POST /v0/management/plugin-store/cpa-grok-panel/install
Authorization: Bearer <management_key>
Content-Type: application/json

{"version":"0.6.0"}
```

版本号为去掉 `v` 前缀的 semver，须与 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) 已发布 tag 一致。

#### 4. 打开面板

- 侧栏菜单：**Grok 账号**
- 或：`/v0/resource/plugins/cpa-grok-panel/panel`

### 安装方式 B：手动上传 Release zip

适合不改 `store-sources`、离线拷包或商店链路不通。

1. 在 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) 按 CPA 主机架构下载  
   - **Linux x86_64：** `cpa-grok-panel_0.6.0_linux_amd64.zip`  
   - **Linux arm64：** `cpa-grok-panel_0.6.0_linux_arm64.zip`  
   - **Windows x64：** `cpa-grok-panel_0.6.0_windows_amd64.zip`（根目录 `cpa-grok-panel.dll`）  
   - **Windows ARM64：** `cpa-grok-panel_0.6.0_windows_arm64.zip`  
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

- 搜索文件名；筛选项顺序：**存活 → 套餐 → 风控 → 状态 → 降权**  
- 分页 20 / 50 / 100  
- 可排序：账号文件、套餐、存活、状态、风控、优先级、用量、成功 / 失败数  
- 顶部汇总：账号数、观察 / 异常 / 死号 / 正常、成功 / 失败请求、累计 Token  
- `is_demoted`：class ∈ {watch, anomaly, dead} 且 state 为 applied/requested（**不以** `priority ≤ dead_priority` 误伤低 baseline）

### 套餐、存活与测活

| 项 | 行为 |
| --- | --- |
| **套餐列** | `Unknown`（plan=`unknown`）/ Free / SuperGrok / SuperGrok Heavy |
| **存活列** | 存盘小写 `probe_status`，UI 见下表 |
| **批量刷新套餐** | GET billing 经 `api-call` 落盘；**保留** `probe_*` |
| **批量测活** | 面板 `api-call` → `POST /accounts/apply-probe`（`source=manual`）；**只测活，不刷套餐**；envelope **`data`=JSON 字符串**（勿写 `body`） |
| **自动测活** | Go：token + auth `proxy_url` > `outbound_proxy_url` > 环境变量；同一 responses body |
| **刷新成功** | SuperGrok / SuperGrok Heavy 按证据映射；其余成功记 **Free** |
| **刷新失败** | 记 **unknown** |
| **缓存** | 套餐与测活写入插件 state；列表轮询不改写 |
| **用量列** | Free 或无官方限额时：用量 = 插件日 token，限额 = Free 日限额（默认 2M） |

#### 存活 `probe_status`

| 条件 | status | UI |
| --- | --- | --- |
| 2xx | `live` | Live 绿 |
| 401 | `exceed` | Exceed 黄 |
| 403 | `dead` | Dead 红 |
| 429 | `cooling` | Cooling 黄 |
| 其它 | `error` | Error 黄 |
| 空/未测 | `""` | **Unknown** 灰 |

#### 测活结果应用 `ApplyProbeResult(auth, result, source)`

| source + 结果 | 行为 |
| --- | --- |
| **manual + live** | 只更新存活，**不改** priority/class |
| **manual + 非 live** 或 **auto** | 按结果改档（见下） |
| auto + live（当前 watch 复测） | 恢复 **none** + `default_restore_priority` + 清 NextProbeAt |
| auto + live（其它） | class=**watch**，priority=`watch_priority`，NextProbeAt=+`watch_reprobe_minutes` |
| exceed / dead | class=**dead**，priority=`dead_priority`，清 NextProbeAt；自动任务 skip |
| cooling / error | class=**anomaly**，priority=`anomaly_priority`，NextProbeAt=+`anomaly_reprobe_hours` |

### 单账号操作

| 操作 | 行为 |
| --- | --- |
| **启用 / 停用** | Management status API |
| **降权** | 写死号档 `dead_priority`，记录写前优先级为基线（诊断用） |
| **解除降权** | `default_restore_priority`（默认 0，**不**记 baseline）+ 清 debt + class=none + 存活→Unknown + 取消 NextProbeAt |
| **安全删除** | 须输入精确文件名；映射变化则跳过 |

### 批量操作

- 表头选当前页；「全部选中」= 当前筛选结果  
- 支持：启用、停用、降权、解除降权、批量测活、批量刷新套餐、批量重签、设置优先级、安全删除  
- 解除/手动降权走 `POST /accounts/restore-priority` 与 `POST /accounts/demote`  
- 批量测活 `source=manual`；api-call **`data` 字符串**（v0.5.11 起）  
- 有限并发（默认 10）；测活并发更保守（约 3）

### 自动刷新（列表）

默认开启、间隔 5 秒；仅页面可见且无账号操作时执行。**只刷新列表 / 设置 / meta，不刷新套餐。**  
可在设置页关闭或改为 2–60 秒。

### 优先级调度（积分 → 测活 → 观察/异常/死号）

目标：让失败 auth 少被 CPA 选中。插件**不实现 CPA 路由**，只写 `priority`。v0.6.0 去掉 soft/hard 双阈值与 half-open / cooldown 阶梯。

| class | 中文 | 默认 priority |
| --- | --- | ---: |
| `none` | 正常 | `default_restore_priority` = 0 |
| `watch` | 观察 | `watch_priority` = -10 |
| `anomaly` | 异常 | `anomaly_priority` = -50 |
| `dead` | 死号 | `dead_priority` = -100 |

**积分策略（仅 class=`none`）**

| 事件 | 行为 |
| --- | --- |
| **success** | debt 衰减；probe 非 live → 标 live；若 class≠none → 恢复正常（`default_restore_priority`、清档、**清 debt**、取消定时复测） |
| **failure**（401/403/429 计分沿用） | 仅 none 加分；**dead 冻结积分**；watch/anomaly **不加分、不因积分触发** |
| debt ≥ `debt_probe_threshold`（默认 2.0） | **debt=0** → 自动测活（`source=auto`） |
| 连败 ≥ `attributed_failure_threshold` | 同样触发自动测活（探测决定档位，不直接写死） |

**观察 / 异常定时复测**

- Worker 扫描 `NextProbeAt ≤ now` 且 class ∈ {watch, anomaly}（跳过 dead）  
- 执行 auto 测活 → `ApplyProbeResult(auto)`  
- watch 复测仍 live → 正常档 + `default_restore_priority` + 清 NextProbeAt  

priority 由 demotion worker **异步**写入（优先 Management fields；否则 `host.auth.save`）。

### 每日清零

默认关，默认 `00:00`（插件进程本地时区）。清零请求数、Token 累计与连败 streak；failure debt 保留。

### 诊断列与风控列

**诊断**：failure debt、连败、class（none/watch/anomaly/dead）、下次复测时间等。

**风控**（原「机器人」列，移到状态前；只读解析 JWT `bot_flag_source`，不写 state）：

| 显示 | 条件 |
| --- | --- |
| 红「是」 | `bot_flag_source` 为 `1` / `"1"` |
| 绿「否」 | 有效 token 且无标记 |
| 灰「Unknown」 | 无 token、无效 JWT 或读失败 |

## 设置与环境变量

面板保存过设置后，以 state 中的 settings 为准。下列环境变量**仅在首次启动且尚无持久化设置时**作初始值：

| 环境变量 / 设置项 | 默认 | 说明 |
| --- | ---: | --- |
| `CPA_GROK_BATCH_CONCURRENCY` | `10` | 批量操作并发 1–50 |
| `CPA_GROK_FAILURE_THRESHOLD` | `3` | 连败阈值（触发自动测活）1–100 |
| `debt_probe_threshold` / `CPA_GROK_DEBT_PROBE_THRESHOLD` | `2.0` | 积分阈值：≥ 则清零 debt 并自动测活 |
| `debt_fail_401` / `CPA_GROK_DEBT_FAIL_401` | `1.5` | 401/403 debt 加分 |
| `debt_fail_429` / `CPA_GROK_DEBT_FAIL_429` | `0.5` | 开启 429 计数时的 debt 加分 |
| `debt_success_decay` / `CPA_GROK_DEBT_SUCCESS_DECAY` | `1.0` | 成功请求 debt 衰减 |
| `watch_priority` / `CPA_GROK_WATCH_PRIORITY` | `-10` | 观察档目标优先级 |
| `anomaly_priority` / `CPA_GROK_ANOMALY_PRIORITY` | `-50` | 异常档目标优先级 |
| `dead_priority` / `CPA_GROK_DEAD_PRIORITY` | `-100` | 死号目标优先级（旧 `CPA_GROK_DEMOTION_PRIORITY` 为别名） |
| `default_restore_priority` / `CPA_GROK_DEFAULT_RESTORE_PRIORITY` | `0` | 手动解除 / 成功恢复 / watch 复测成功 的写回优先级 |
| `watch_reprobe_minutes` / `CPA_GROK_WATCH_REPROBE_MINUTES` | `30` | 观察档定时复测间隔（分钟） |
| `anomaly_reprobe_hours` / `CPA_GROK_ANOMALY_REPROBE_HOURS` | `6` | 异常档定时复测间隔（小时） |
| `CPA_GROK_COUNT_429` | `false` | 429 是否计入连败 / debt |
| `CPA_GROK_COUNT_5XX` | `false` | 5xx 是否计入连败 |
| `CPA_GROK_MANAGEMENT_BASE_URL` | 未设置 | 自动改 priority 用的 CPA 地址 |
| `CPA_GROK_MANAGEMENT_KEY` | 未设置 | Management fields key；须与 BASE 成对，否则回退 `host.auth.save` |
| `CPA_GROK_OUTBOUND_PROXY` | 未设置 | 批量重签 / 自动测活出站代理（auth `proxy_url` > 设置页 `outbound_proxy_url` > 本变量 > HTTPS_PROXY） |

**已移除 / 忽略（v0.6.0）**：`soft_demotion_*`、`hard_debt_threshold`、`half_open_*`、`cooldown_restore_*`（JSON 旧字段可读可忽略；设置页已删除相关表单）。

设置页还可改：自动刷新、每日清零、**Free 用户日限额（默认 2000000）**、**出站代理** 等，热生效。  
浏览器本地（不写插件 state）：**外观 / 主题** = 跟随 CPA / 暗色 / 亮色。  
`CPA_GROK_MANAGEMENT_*` 变更后需重启插件进程。

## Changelog

### v0.6.0

- **降权模型重做**：`none` / `watch`（观察）/ `anomaly`（异常）/ `dead`（死号）替代 soft/hard/half_open；旧 state soft→watch、hard→dead、half_open→watch
- **积分策略**：仅正常档吃阈值；`debt_probe_threshold`（默认 2.0）→ 清零 debt → 自动测活；死号冻结积分；观察/异常不加分
- **测活改档**：`ApplyProbeResult`；手动 Live 不改档；手动非 Live 与自动相同改档；成功请求 → Live + 恢复正常 + 清积分 + 取消复测
- **手动解除降权**：`default_restore_priority` + 清积分/档 + 存活 Unknown（不记 baseline）
- **定时复测**：watch 默认 30 分钟、anomaly 默认 6 小时；Go 侧自动测活（token + proxy）
- **存活 UI**：Live / Exceed / Dead / Cooling / Error / Unknown；套餐 unknown 显示 Unknown
- **UI**：风控列+筛选；筛选项顺序 存活→套餐→风控→状态→降权；设置页新参数
- **API**：`POST /accounts/apply-probe`；面板测活仍 `api-call` + `data` 字符串

## 开发与发版

### 构建与测试

```bash
# Linux amd64
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o dist/cpa-grok-panel.so .

# Linux arm64（需 aarch64-linux-gnu-gcc）
CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
  go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o dist/cpa-grok-panel_arm64.so .

CGO_ENABLED=1 go test ./...
go vet ./...
```

### 发版打包

商店 / 手动安装认 **按架构命名的 zip**（zip 根目录必须是 `cpa-grok-panel.so`）：

```text
cpa-grok-panel_<semver>_linux_amd64.zip
cpa-grok-panel_<semver>_linux_arm64.zip   # 有交叉编译链时一并产出
checksums.txt
```

**不要只上传裸 `cpa-grok-panel.so`**，否则商店会报 zip not found。

一键打包（本机有 `aarch64-linux-gnu-gcc` 时会同时打 arm64）：

```bash
./scripts/package_release.sh 0.6.0
# 生成例如：
#   dist/cpa-grok-panel_0.6.0_linux_amd64.zip
#   dist/cpa-grok-panel_0.6.0_linux_arm64.zip
#   dist/checksums.txt

gh release upload v0.6.0 \
  dist/cpa-grok-panel_0.6.0_linux_amd64.zip \
  dist/cpa-grok-panel_0.6.0_linux_arm64.zip \
  dist/checksums.txt \
  --clobber
```

## 相关文档

- 架构与集成：[docs/design/](docs/design/)
- 评审与探测：[docs/reviews/](docs/reviews/)
- 发行版：[Releases](https://github.com/magicvr/cpa-grok-panel/releases)

README 以当前可安装版本 **v0.6.0** 为准。
