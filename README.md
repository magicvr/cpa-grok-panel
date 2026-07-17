# cpa-grok-panel

## 友链

> 感谢 [LINUX DO](https://linux.do/) 社区对开源项目的支持。本项目的开发与推广得益于社区环境，在此致敬。

[![LINUX DO](https://img.shields.io/badge/LINUX%20DO-社区友链-0066cc)](https://linux.do/)

---

`cpa-grok-panel` 是 CLIProxyAPI（CPA）的 Grok/xAI OAuth 账号运维面板。v0.3.8 新增“优先级冷却恢复”：降权后按 6h → 12h → 24h 阶梯自动恢复，明确标记为机器人的账号除外。

插件 id：`cpa-grok-panel`。

## 功能概览

- **账号列表**：读取 CPA 中的 xAI OAuth 账号，展示机器人标记、启停状态、优先级、请求数和 Token 用量。
- **用量统计**：累计 CPA `usage` 回调中的真实 input、output 和 total token，不根据请求内容估算。
- **账号操作**：支持单账号和批量启用、停用、降权、解除降权，以及批量设置优先级。
- **自动降权**：401/403 始终计入连续失败；429 和 5xx 可选择计入同一阈值路径，默认连续 3 次后请求降权。
- **优先级冷却恢复**：默认开启，降权后按 6h → 12h → 24h（封顶 24h）恢复优先级并清空失败诊断；再次降权递增冷却。
- **安全删除**：删除前重新校验 `auth_index` 与精确文件名映射；删除成功后清理插件本地账号状态。
- **每日清零**：可按服务器本地时区每天清零请求数、Token 累计和连续失败计数。
- **持久化设置**：面板设置和统计状态保存在插件 state 中，重启后继续生效。

当前未实现主动健康检查、套餐核验和 Responses 实测。CPA 目前未向插件提供所需的 `host.auth.invoke` 能力，面板不会伪造这些结果。

## 安装

### 前置条件

- CPA 已启用插件体系（`plugins.enabled: true`），并支持原生插件 ABI、Management API 和 usage 回调。
- 已设置并持有 CPA management key。
- 运行平台为 **Linux amd64**（当前仅发布该平台的 Release 包）。
- CPA 进程所在主机能访问 **GitHub**（`api.github.com`、`github.com`、`raw.githubusercontent.com`、Release 资源域名）。若主机直连 GitHub 不稳定，请在 CPA 配置中设置可用的 `proxy-url`，再走安装方式 A。

### 安装方式 A（推荐）：把本仓库 registry 加入 CPA 插件商店

安装方式 A 的思路是：向 CPA 注册一个**商店目录源**（`store-sources`），让 CPA 插件商店能**列出**本插件；真正安装时，CPA 再按目录里的 `repository` 字段去对应仓库的 **GitHub Releases** 拉取 zip 与校验文件。

```text
store-sources → registry.json（只负责目录/元数据）
plugin.repository → GitHub Releases（真正下载 .so 安装包）
```

#### 1. 商店目录 URL

本仓库公开目录文件：

```text
https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

当前 `registry.json` 中的关键字段：

| 字段 | 值 |
| --- | --- |
| `id` | `cpa-grok-panel` |
| `name` | Grok 账号面板 |
| `version` | 与最新 Release 对齐（例如 `0.3.8`） |
| `repository` | `https://github.com/magicvr/cpa-grok-panel` |

可用浏览器或下面命令确认目录可访问、内容正确：

```bash
curl -fsSL https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
```

#### 2. 在 CPA 配置里添加 `store-sources`

编辑 CPA 的 `config.yaml`（路径以你的部署为准），在 `plugins` 段加入或合并如下内容：

```yaml
plugins:
  enabled: true
  # dir: plugins          # 保持你现有的插件目录即可
  store-sources:
    # 可与其它商店源并存；本插件只需加入下面这一条
    - https://raw.githubusercontent.com/magicvr/cpa-grok-panel/main/registry.json
  configs:
    cpa-grok-panel:
      enabled: true
```

说明：

- `store-sources` 是字符串数组，每项是一个 **registry.json 的 HTTPS URL**。
- 若你已有其它商店源，**追加**本 URL，不要整段覆盖丢掉原有源。
- `configs.cpa-grok-panel.enabled: true` 表示安装后允许启用该插件；若你的 CPA 版本在商店安装时会自动写入，也可先只加 `store-sources`，安装后再确认。
- 修改 `config.yaml` 后，按你的 CPA 版本要求**重载配置或重启一次**，让新的商店源生效。

> 若你的 CPA 管理页提供「插件商店源 / store sources」类设置，效果应等价于写入上述 `store-sources`：把同一 URL 粘贴进去保存即可。以你当前 CPA 管理页实际字段为准。

#### 3. 在插件商店里找到并安装

1. 打开 CPA 管理页（例如 `http://<cpa-host>:<port>/management.html`），使用 management key 登录。
2. 进入 **插件 / 插件商店**（Plugin Store）相关页面。
3. 刷新商店列表后，应能看到 **Grok 账号面板** / id **`cpa-grok-panel`**。
4. 选择需要的版本（一般选最新，例如 `0.3.8`）并点击安装。
5. 安装成功后，**完整停止并重新启动整个 CPA 进程**。  
   本插件是原生 `.so`：仅热更新、只重载配置或不杀进程替换动态库，可能导致仍加载旧库或注册状态异常。

也可用 Management API 安装（需已能在商店目录中解析到该插件）：

```http
POST /v0/management/plugin-store/cpa-grok-panel/install
Authorization: Bearer <management-key>
Content-Type: application/json

{"version":"0.3.8"}
```

版本号请与 [Releases](https://github.com/magicvr/cpa-grok-panel/releases) 上已发布的 tag（去掉前缀 `v` 后的 semver）一致。

#### 4. 安装后如何打开面板

- 管理页菜单：**Grok 账号**（若侧栏已出现）
- 或直接访问资源路径：

  ```text
  /v0/resource/plugins/cpa-grok-panel/panel
  ```

#### 5. 方式 A 常见问题

| 现象 | 常见原因 | 处理 |
| --- | --- | --- |
| 商店里搜不到本插件 | 未写入 / 未生效 `store-sources`，或 registry URL 写错 | 核对 URL 与配置重载；用 `curl` 验证 registry.json |
| 能列出但安装失败（GitHub 404） | 仓库不可见、Release 不存在、或 CPA 未拿到公开资源 | 确认仓库公开且对应版本已发 Release；检查 CPA 出网/代理 |
| 安装失败（403 / rate limit） | 未认证访问 GitHub API 配额或代理拦截 | 等待重试、配置 `proxy-url`、检查出口 IP 限制 |
| 安装显示成功但 `registered: false` / 刷新后变未注册 | 动态库未完整加载或 reconfigure 异常 | **完整重启 CPA**；再查 `GET /v0/management/plugins` |
| 面板仍是旧版本 | 热替换 `.so` 或浏览器缓存 | 完整重启 + 浏览器强刷；看面板副标题版本号 |

### 安装方式 B：手动下载 GitHub Release 安装

适合：暂时不想改 `store-sources`、离线拷包、或商店安装链路不通时。

1. 打开 [GitHub Releases](https://github.com/magicvr/cpa-grok-panel/releases)，下载当前平台包，例如：

   - `cpa-grok-panel_0.3.8_linux_amd64.zip`
   - （可选校验）同 Release 下的 `checksums.txt`

2. 在 CPA **插件管理**中选择本地安装 / 上传该 zip。  
   **不要**改压缩包内部结构：包根目录应直接是 `cpa-grok-panel.so`（不要再套一层额外目录后再打包）。

3. 安装完成后同样 **完整停止并重新启动 CPA**。

4. 打开面板（与方式 A 相同）：

   ```text
   /v0/resource/plugins/cpa-grok-panel/panel
   ```

### 安装方式对照

| | 安装方式 A（商店源） | 安装方式 B（手动包） |
| --- | --- | --- |
| 需要改 CPA 配置 | 是：追加 `store-sources` | 否 |
| 升级体验 | 商店内选版本安装 | 每次手动下新 zip |
| 依赖出网 | 需要访问 GitHub（目录 + Release） | 可在有网机器下载后拷到 CPA 主机 |
| 安装后 | 都必须**完整重启 CPA** | 同左 |

## 使用

### 首次打开

进入面板的 **设置** 页，填写 CPA management key 并保存。密钥只写入当前浏览器的 `localStorage`，不会写入插件 state；清理浏览器数据或更换浏览器后需要重新填写。

### 账号列表

- 可按文件名搜索，按启停状态、是否降权或机器人检测结果筛选。
- 支持 20、50、100 条分页，可跳转首页、末页或指定页，并可按账号文件、状态、机器人检测结果、优先级、总 Token、成功数或失败数排序。
- 顶部汇总显示账号数、已降权数、成功/失败请求数和累计 Token。
- “已降权”按当前设置判断：`priority <= demotion_priority`。

### 单账号操作

- **启用/停用**：通过 CPA Management API 修改账号运行状态。
- **降权**：通过 CPA Management fields API 写入降权目标，成功后保存写前优先级作为恢复基线。
- **解除降权**：通过 CPA Management fields API 优先恢复已记录的基线；没有可靠基线时使用“默认恢复优先级”。
- **人工解除降权优待**：立即恢复并将冷却阶梯重置为 0；即使账号标记为机器人也可人工解除降权。
- **诊断清理**：启停、手动降权或解除降权成功后清空连败、上次失败时间和失败码；自动降权保留诊断。
- **安全删除**：必须输入账号的精确文件名确认。插件会在删除前重新核对账号映射，映射已变化时跳过删除。

### 批量操作

- 表头复选框选择当前页；“全部选中”选择当前筛选结果；“清除选中”取消所有选择。
- 支持批量启用、停用、降权、解除降权、设置优先级和安全删除；批量设置优先级要求输入整数，并通过 CPA Management fields API 按精确文件名写入。
- 批量操作使用有限并发执行并显示完成进度以及成功、跳过和失败数量；默认并发为 10，可在设置页调整为 1–50。批量删除要求输入 `DELETE`，且每个账号删除前都会再次校验映射。

### 自动刷新

自动刷新默认开启，间隔为 5 秒，仅在页面可见且没有账号操作时执行。可在设置页关闭或将间隔调整为 2 至 60 秒；关闭后账号页右上角会显示手动“刷新”按钮。

### 自动降权

- 归因到账号的 HTTP 401/403 始终计入连续失败阈值，不再单次立即降权。
- 所有可计数状态共用连续失败阈值，默认阈值为 3；429 和 5xx 默认不计数，可在设置页分别开启。
- 默认降权优先级为 `-100`。面板以 `priority <= demotion_priority` 判定账号已降权。
- 自动降权优先使用可选的 Management fields HTTP 写入，未配置时回退到 `host.auth.save`；两种路径都会重新读取账号列表校验结果。
- 已记录为 `applied` 但宿主优先级高于当前降权目标时，列表读取或 worker 周期会重新请求降权，面板也会标记“记录陈旧/host未降权”。
- “优先级冷却恢复”默认开启；自动恢复保留当前冷却阶梯，下一次降权继续递增，明确标记为机器人的账号不会自动恢复。

### 每日清零

每日清零默认关闭，默认时间为 `00:00`，使用插件进程所在服务器的本地时区。启用后会清零请求数、Token 累计和连续失败计数；插件启动时若当天已过设定时间且尚未执行，会补执行一次。

### 诊断列

诊断列直接显示连续归因失败次数、上次失败码，以及处理中、已降权或失败标记。将鼠标停在该列可查看上次失败时间、降权状态、目标优先级、恢复基线、触发时间和降权失败码。

“机器人”列只读解析账号 `access_token` 的 JWT payload；`bot_flag_source` 为数字 `1` 或字符串 `"1"` 时显示红色“是”，有效 token 无标记时显示绿色“否”，无 token、无效 JWT 或单账号凭据读取失败时显示灰色“—”。列表读取凭据使用有限并发，且不会写入插件 state。

## 设置与环境变量

面板保存过设置后，以持久化 state 中的 settings 为准。以下 `CPA_GROK_*` 环境变量只在插件首次启动且尚无持久化设置时作为初始值生效：

| 环境变量 | 默认值 | 说明 |
| --- | ---: | --- |
| `CPA_GROK_BATCH_CONCURRENCY` | `10` | 首次无持久化设置时的浏览器批量操作并发数，范围 1–50 |
| `CPA_GROK_FAILURE_THRESHOLD` | `3` | 所有可计数状态共用的连续失败阈值，范围 1–100 |
| `CPA_GROK_DEMOTION_PRIORITY` | `-100` | 自动或手动降权的目标优先级 |
| `CPA_GROK_DEFAULT_RESTORE_PRIORITY` | `0` | 没有可靠基线时的恢复优先级 |
| `CPA_GROK_COOLDOWN_RESTORE` | `true` | 是否默认开启优先级冷却恢复 |
| `CPA_GROK_COUNT_429` | `false` | 是否将 429 计入连续失败阈值 |
| `CPA_GROK_COUNT_5XX` | `false` | 是否将 5xx 计入连续失败阈值 |
| `CPA_GROK_MANAGEMENT_BASE_URL` | 未设置 | 自动降权/冷却恢复使用的 CPA 地址，例如 `http://127.0.0.1:8317`；需与 key 同时设置 |
| `CPA_GROK_MANAGEMENT_KEY` | 未设置 | 自动降权/冷却恢复调用 Management fields API 的 key；未成对设置时回退 `host.auth.save` |

自动刷新和每日清零可直接在设置页配置，无需重启插件。Management 地址和 key 由插件进程环境读取，变更后需重启。

## 开发与构建

插件使用 Go + CGO，以 `c-shared` 模式构建原生动态库。Linux amd64 的基本构建命令：

```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -buildmode=c-shared -o cpa-grok-panel.so .
```

运行测试：

```bash
go test ./...
```

架构、CPA 集成、接口和持久化等设计资料仍保留在 [docs/design/](docs/design/)；README 以当前 v0.3.8 的可安装版本为准。
