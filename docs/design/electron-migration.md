# 用 Electron 重构桌面端：计划

状态：计划已定；**阶段 0（解耦）和阶段 1（最小壳）已实现**，阶段 2 的打包配置、工作流、CSP 和实测数据已完成，阶段 3 的更新机制已实现（Electron 渠道的发布与老用户迁移提示待阶段 5 前后接上），阶段 4 已实现托盘、后台运行、通知、全局快捷键、桌面设置页（多窗口只做了 Git 窗口），阶段 5 进行中：Electron 安装包已并行发布，默认入口仍是 Wails。本文是迁移的依据，与代码不一致时以代码为准。

## 1. 为什么要做

不是因为 Wails 有 bug，而是产品要往这几个方向走，Wails v2 做不到或做起来很别扭：

| 方向 | Wails v2 | Electron |
| --- | --- | --- |
| 接入 Claude Code / pi 等外部 harness（官方 SDK 只有 TS / Python） | 要另外拉一个 Node 进程，用户还得自己装 Node | 主进程就是 Node，SDK 直接跑；Node 随应用发布 |
| 内置可控的浏览器（agent 打开、点击、截图网页，预览自己写的页面） | 系统 WebView 做不到可编程控制 | 嵌入受控的 Chromium（`WebContentsView` + CDP） |
| 托盘、关窗后会话在后台继续、跑完通知 | 没有托盘 | 原生支持 |
| 多窗口（Git、产物预览、单个会话拆出去） | 基本是单窗口 | 原生支持 |
| 全局快捷键、快速提问 | 没有 | 原生支持 |
| 三个平台渲染一致 | WebView2 / WKWebView / WebKitGTK 三套引擎，Linux 有黑窗、WebKitGTK 版本问题 | 同一个 Chromium |

代价（接受）：安装包大约多 70–110MB、内存更高（**未实测**，阶段 2 实测；对照基线：本地交叉编译的 Wails Windows 版 `mosaic.exe` 是 30.4MB，含嵌入的前端）；Windows 从"一个 exe"变成安装包；自动更新要重做。

## 2. 不做什么

- **不把 Go 内核重写成 TS。** 24k 行 Go（harness、厂商适配、MCP、权限、插件、存储、更新校验）和 333 个测试是资产，重写不带来体验或扩展性的收益，只带来回归。Go 编译成独立程序 `mosaicd`，作为 Electron 的子进程运行。
- **不改前端。** 前端只通过 `Backend` 接口（`frontend/src/bridge/types.ts`）和后端通信，Wails 相关的只有 `bridge/wails.ts` 一个文件（275 行）。迁移只新增一个 `bridge/electron.ts`。
- **不改用户数据。** 数据目录 `~/.myllmapp`（`sessions.db`、`settings.json`、插件、ChatGPT 凭据）和钥匙串条目都由同一份 Go 代码读写，没有数据迁移。
- Go 的 CLI（`cmd/cli`）保持不变。

## 3. 目标架构

```mermaid
flowchart LR
    subgraph Electron
        MAIN["主进程（TS）<br/>窗口 · 托盘 · 菜单 · 对话框<br/>单实例 · 快捷键 · 更新"]
        PRE["preload<br/>contextBridge: window.mosaic"]
        R["渲染进程<br/>现有 React 前端<br/>bridge/electron.ts"]
        HU["utilityProcess（阶段 6）<br/>Claude Agent SDK · pi SDK"]
    end
    GO["mosaicd（Go 子进程）<br/>internal/app + 全部内核"]
    R -- "ipcRenderer.invoke('mosaic:call')" --> PRE
    PRE --> MAIN
    MAIN -- "stdin：JSON-RPC 请求 / 回应" --> GO
    GO -- "stdout：回应 / event 通知 / host.* 反向请求" --> MAIN
    MAIN -- "webContents.send('mosaic:event')" --> R
    GO -. "harness.*（阶段 6）" .-> MAIN
    MAIN -.-> HU
```

- 渲染进程：`contextIsolation: true`、`sandbox: true`、`nodeIntegration: false`，只能通过 preload 暴露的 `window.mosaic.call(method, args)` 和 `window.mosaic.onEvent(cb)` 访问后端。主进程只转发方法名在白名单里的调用（白名单 = `mosaicd` 在握手时报告的方法列表）。
- `mosaicd` 不开任何网络端口：和主进程之间只用 stdio。其他本机进程、网页都连不上它。
- 主进程替 Go 做那几件"必须由窗口来做"的事（见 §4.3 的 `host.*`）。

## 4. 协议（阶段 0 已实现：`internal/rpc`、`cmd/mosaicd`）

### 4.1 帧格式

- JSON-RPC 2.0，**一行一条消息**（`\n` 分隔），UTF-8。stdout 只写协议消息，日志一律写 stderr。
- 单条消息上限 64MB（会话历史、图片附件可能很大）。
- stdin 读到 EOF（Electron 主进程退出、崩溃）时，`mosaicd` 取消所有运行中的会话并退出：不会留下孤儿进程。

### 4.2 前端 → Go：调用绑定方法

- 方法名就是 `internal/app.App` 的公开方法名（与 Wails 绑定同名），例如 `GetStatus`、`SendToSession`。
- `params` 是**按位置的数组**，与 Go 方法的参数一一对应；省略的参数按零值处理。返回值：方法返回 `(T, error)` 时 `result` 是 `T`；返回 `error` 时 error 非空就是 JSON-RPC 错误（`code: -32000`，`message` 是错误文本，和 Wails 里 Promise 被 reject 的文本一致）。
- 每个请求在独立的 goroutine 里执行（和 Wails 一样可以并发），回应按完成顺序写出，用 `id` 配对。
- 保留的方法：
  - `rpc.hello` → `{"protocol":1,"event_protocol":1,"version":"1.7.10","methods":[…]}`：握手，主进程据此确认版本并建立白名单；
  - 不可调用：`Startup`、`Shutdown`、`SetHost`、`SetPlugins`、`SetChangelog`、`UseShellUpdater` 这些生命周期方法。

### 4.3 Go → Electron

- **事件通知**：`{"jsonrpc":"2.0","method":"event","params":{"name":"agent:event","data":{…}}}`。`data` 就是现在 `agent:event` 的事件信封（21 种类型、带 `seq`），前端代码不变。注意事件由多个 goroutine 发布（agent 循环、回答权限确认的那次调用等），写到 stdout 的顺序不一定等于 `seq` 的顺序，所以前端的 `reorder.ts` 仍然需要（测试里实测到过 seq 16 晚于 18 到达）。
- **反向请求**（Go 等待回应）：
  - `host.openFile`（`{title, default_directory, filters:[{display_name, pattern}]}` → 路径或 `""`）；
  - `host.openDirectory`（`{title, default_directory, can_create_directories}` → 路径或 `""`）；
  - `host.saveFile`（`{title, default_filename, filters}` → 路径或 `""`）。
- **反向通知**（不等回应）：`host.openURL`（`{"url": "https://…"}`，主进程只放行 http / https）、`host.quit`。

这些正好对应现在 `internal/app` 里直接调用 Wails 的 10 处（3 个对话框、4 处打开网址、1 处退出、1 处推事件），阶段 0 把它们都收进了 `app.Host` 接口。

## 5. 分阶段（每一阶段都能独立运行和验证）

### 阶段 0：解耦（已完成）

- `internal/app` 不再 import Wails：桌面能力都走 `app.Host` 接口；Wails 的实现放在仓库根的 `main` 包（`wails_host.go`），行为不变。
- `internal/rpc`：通用的 stdio JSON-RPC 对等端（请求、通知、反向请求，反射分发方法）。
- `cmd/mosaicd`：读配置 → 组装内核 → 在 stdio 上服务 `App` 的全部方法。更新改由壳安装：后端 `UseShellUpdater` 只检查、下载并校验，再请壳运行安装程序（见阶段 3）。
- **验证**：现有测试全部通过；`cmd/mosaicd` 的测试用演示厂商通过 stdio 跑完整的一轮（新建会话 → 发消息 → 收事件 → 回答权限确认 → `turn_end`），并覆盖反向请求；`scripts/mosaicd-smoke.mjs` 用 Node 从外部跑同样的流程（这就是 Electron 主进程将来要做的事）；`internal/archtest` 禁止 `internal/` 再导入 Wails。Wails 版照常构建和发布。

### 阶段 1：Electron 最小壳（已完成）

- `desktop/`：`src/main.ts`（窗口、单实例锁、白名单转发、导航限制）、`src/preload.ts`（`window.mosaic`）、`src/sidecar.ts`（拉起 `mosaicd`、握手、白名单、事件、退出）、`src/host.ts`（对话框、打开链接）、`src/rpc.ts`（协议）、`src/smoke.ts`（冒烟）。
- 前端新增 `bridge/electron.ts`，与 Wails 版共用方法映射表（`wails.ts` 的 `createBackendFrom`）；`bridge/index.ts` 在 Electron / Wails / 演示模式里选一个。前端其余代码没改。
- 前端用自定义协议 `mosaic://app/` 提供（不是 `file://`）：构建产物里的资源用绝对路径，而且 ES 模块脚本在 `file://` 下会被当成跨域；自定义协议下有正常的来源。
- 安全：`contextIsolation`、`sandbox`、无 Node；IPC 只接受来自我们自己页面的调用；方法名只放行后端握手时报告的列表；页面里的链接交给系统浏览器（只放行 http / https），窗口不能被导航走。**页面有 CSP**（`desktop/src/main.ts` 的 `CSP`）：脚本只能来自打包的前端（加 `'wasm-unsafe-eval'` 给 Graphviz），不允许 eval 和内联脚本，连接只留自己和预览协议；在真实窗口里验证过 Mermaid / Graphviz(wasm) / KaTeX 在 CSP 下都能渲染，且没有违规。产物面板里的网页预览走独立的 `mosaic-preview://` 协议（`desktop/src/preview.ts`）：srcdoc 会继承页面 CSP 导致“允许脚本”的预览全坏，独立来源则不继承，隔离改由响应头的 CSP `sandbox`（没有 `allow-same-origin`）保证，强度和 Wails 版一样；往里存网页要带一次运行一个的随机令牌，只有应用页面的 preload 拿得到（实测自定义协议的跨源请求 Origin 是 `null`、没有 Referer，光看请求头判断不了来源）。
- 开发：`make desktop-install`；`make desktop-start`（先构建前端，后端用 `go run` 或 `MOSAIC_MOSAICD`）；`MOSAIC_DEV_URL=http://localhost:5173` 时连 vite 开发服务器。
- **验证（已完成）**：`desktop` 的单元测试 10 个（协议分片和 U+2028、id 配对、断线、链接白名单、对话框过滤器，以及用真实 `mosaicd` 验证握手、白名单、一轮对话、反向请求、退出和崩溃）；**冒烟测试在真实的 Electron 45（Chromium 156）窗口里**加载前端、渲染出界面、通过 IPC 跑完一轮演示对话（2 次权限确认），并确认生命周期方法被拒绝。CI 的 `desktop` 任务在三个平台跑单元测试，在 Linux 上用 xvfb 跑冒烟。Windows / macOS 上的真实窗口还需要人工验证（对话框、托盘等）。

### 阶段 2：打包与 CI（并行发布）

- `electron-builder`：Windows NSIS 安装包、macOS `.dmg` / `.zip`（universal：两个架构的 `mosaicd` 用 `lipo` 合并）、Linux AppImage。`mosaicd` 放在 `resources/` 下，不进 asar。
- `release.yml` 增加 Electron 产物，**与 Wails 产物并行发布**；Electron 版先以 `-beta` 预发布 tag 发给愿意尝鲜的用户（正式版用户收不到预发布）。
- **已实现（本阶段第一步）**：`desktop/electron-builder.yml` + `desktop/scripts/stage.mjs`（构建前端和 `mosaicd`，macOS `--universal` 用 `lipo` 合并）+ 独立工作流 `.github/workflows/desktop-release.yml`。没有改 `release.yml`：Electron 版用 `electron-v<版本>` 的预发布 tag（不匹配 `v*`，不会触发 Wails 的发布流程，也不会成为"最新版本"），手动运行并勾选 publish 才创建；改打包配置的 PR 会自动跑一遍三平台打包（不发布）。安装包暂不签名。
- **已验证**：Linux 上 `electron-builder --dir` 产出的目录包，用 xvfb 跑冒烟通过（后端从 `resources/mosaicd` 启动，前端从 `resources/frontend`）。Windows / macOS 的产物由 CI 验证。
- **实测（Linux、xvfb 软件渲染、4 核；打包后的行为与此接近，Windows / macOS 未测）**：
  | 项目 | Wails 版 | Electron 版 |
  | --- | --- | --- |
  | 安装包（Windows） | 23.9 MB | 133 MB（NSIS） |
  | 安装包（Linux） | 23.1 MB | 140 MB（AppImage） |
  | 安装包（macOS） | 17 MB（zip） | 249 MB（dmg）/ 242 MB（zip） |
  | 启动到界面加载完成 | 未测（Linux 上没有 Wails 运行环境） | 约 0.25–0.35 秒（含启动后端、握手，进程启动算起） |
  | 跑完一轮演示对话后所有 Electron 进程内存之和 | 未测 | 约 510 MB（工作集，共享页重复计算，偏高；不含 mosaicd） |
  Windows 比计划估的（70–110MB 增量）多出约 110MB，是接受的代价里最明显的一项；内存没有 Wails 版对照，需要在真机上用任务管理器 / 活动监视器对比。
- **验证**：下载三个平台的产物能启动、能对话；实测安装包大小、冷启动时间、空闲和长会话的内存，和 Wails 版对比写进本文。

### 阶段 3：自动更新与从 Wails 迁移

- Electron 版用 `electron-updater`（Windows NSIS、Linux AppImage），**下载后先按现有的 ed25519 签名清单校验 sha256 再安装**——保留现在“只信任内置公钥签过的清单”的安全模型，不因为换壳变弱。macOS 没有签名公证时仍是“提示并打开下载页”（和现在一样）。
- 老用户迁移：最后一个 Wails 版本的清单把所有平台标成“手动更新”，提示下载 Electron 安装包；数据目录和钥匙串不变，装上即用。
- **已实现**：没有引入 `electron-updater`，而是复用现有的 Go 更新逻辑，信任模型一点没变——
  - 后端 `App.UseShellUpdater(dir)`：同一份签名清单里取 `<平台>-electron` 的安装包（Wails 版取不带后缀的单文件，互不干扰），下载到 `os.UserCacheDir()/mosaic/updates`（保留扩展名）并校验大小和 sha256；然后用反向请求 `host.installUpdate` 请壳安装，自己不替换自己。前端仍是原来的“检查更新 → 安装”，一行不改。
  - 壳 `desktop/src/update.ts`：只接受上面下载目录里、后端命名规则（`.myllmapp-update-*`）、扩展名与平台相符的文件，拒绝后端消息里任意指定的路径。Windows 静默运行 NSIS 安装程序（`/S --updated --force-run`）；Linux 仅在从 AppImage 运行时原地覆盖并延迟重启；macOS 与现在一样是手动更新。
  - 发布侧：`mkmanifest sign -asset linux-amd64-electron=…`（平台键本来就是任意字符串，不用改代码）。**还没接进 `release.yml`**——接入点是阶段 5 切换前后：清单里加上 `*-electron` 资产后，Electron 客户端才有东西可更新。
  - 老用户迁移：最后一个 Wails 版本发布时，清单里**不放 Wails 资产**，该平台自动变成“手动更新、打开发布页”（`Check` 对缺资产的平台本来就这样处理，已有测试覆盖），发布页放 Electron 安装包；数据目录和钥匙串不变。
- **验证**：Windows / Linux 上从 Electron vX 自动更新到 vX+1；从最后一个 Wails 版换到 Electron 版后会话、设置、插件、登录状态都在。

### 阶段 4：桌面能力

- 托盘：关窗后在后台继续运行会话，完成 / 需要确认时系统通知，点通知回到对应会话。
- 全局快捷键唤起“快速提问”。
- **已实现**（`desktop/src/{activity,tray,main}.ts`，前端 `App.tsx` 订阅壳的跳转）：
  - 后台运行：关窗时如果还有会话在跑（由 `turn_start` / `turn_end` 事件跟踪），窗口藏到托盘继续跑并提示一次；没有任务在跑就照常退出。托盘菜单：显示 / 快速提问 / 退出。
  - 系统通知：一轮结束（含出错、到步数上限；用户自己取消的不通知）和权限请求，且窗口不在眼前（隐藏、最小化、没有焦点）时弹；点通知回到对应会话。通知里不放对话内容，只有工具名。
  - 全局快捷键：默认 `CommandOrControl+Shift+M`（环境变量 `MOSAIC_QUICK_SHORTCUT` 可改，空串关闭），唤起窗口、新建会话并聚焦输入框；被占用时只打日志，仍可从托盘进入。
  - 安全：跳转消息只有主进程能发（`mosaic:navigate`），界面只认“打开会话 / 新建会话”两种形状。
- **多窗口（Git 窗口已做，产物预览没做）**：Git 视图可以在界面里点“新窗口”拆成独立窗口（`?window=git`，同一个 `mosaicd`、同一套页面，只渲染 Git 视图；事件广播给所有窗口，各窗口自己过滤）。壳只放行 `git` 这一种窗口类型，每种最多一个，再次打开就是带到前面；主窗口真正关掉时整个应用退出。Wails 版仍是页面里的浮层。**产物预览没拆**：产物面板依赖当前会话的状态（文件列表来自会话事件），独立窗口要先决定“窗口跟着哪个会话”，需要单独设计。
- **设置界面**：设置 → 桌面（仅 Electron 版显示）可以改关闭窗口时的行为（有任务在跑才藏到托盘 / 总是藏到托盘 / 直接退出）、是否弹系统通知、是否启用全局快捷键和快捷键写法。这些属于壳，保存在 Electron 的 userData 目录（`shell-settings.json`），不碰 `~/.myllmapp`；改了立即生效。快捷键写法对但被占用/无法注册时，设置保持原样并把原因告诉界面。环境变量 `MOSAIC_QUICK_SHORTCUT` 只改默认值。
- **验证**：逐项手动验收；托盘和通知在三个平台各测一次。

### 阶段 5：切换

- **已实现（并行发布）**：`release.yml` 的 `build-electron` 与 Wails 的 `build` 并行，三平台各出一个 Electron 安装包，随同一个 Release 发布；Windows / Linux 的安装包进签名清单的 `windows-amd64-electron` / `linux-amd64-electron`，所以 Electron 客户端能自动更新，Wails 客户端看不到它们。`build-electron` 是 `continue-on-error`：它失败不会挡住 Wails 版发布。Wails 仍是默认入口，没有删除任何东西。
- **还没做（等真机试用反馈）**：Electron 成为默认发布；Wails 入口（`main.go`、`wails.json`、`bridge/wails.ts`）在一个过渡版本后删除；文档、截图更新。
- **验证**：连续两个正式版没有 Electron 相关的阻塞问题后再删 Wails。

### 阶段 6（扩展，单独设计）

- **外部 harness**：Go 内核抽出 `Harness` 接口（builtin 就是现在的 agent 循环）；Codex 由 Go 直接接 `codex app-server`；Claude Code、pi 的官方 SDK 跑在 Electron 的 `utilityProcess` 里，通过 `harness.*` 反向请求接回 Go，事件和权限确认照常走 Mosaic 自己的界面。
- **内置浏览器工具**：主进程提供受控的 `WebContentsView`，以 MCP 工具的形式暴露给 agent（打开、截图、点击、读 DOM），每次操作照常走权限确认。

## 6. 风险与对策

| 风险 | 对策 |
| --- | --- |
| 两个进程的生命周期（`mosaicd` 崩溃、Electron 退出后残留） | stdin EOF 即退出；主进程监听子进程退出，自动重启一次并提示；Windows 上用 Job Object 保证进程树随主进程结束（阶段 1） |
| 绑定方法签名变化时前端没跟上 | 握手返回方法列表；`cmd/mosaicd` 的测试检查前端 `bridge/types.ts` 用到的方法在列表里都存在（阶段 1 加） |
| 更新机制变弱 | 保留 ed25519 签名清单校验（阶段 3） |
| 安装包和内存变大 | 阶段 2 实测并记录；按需加载 mermaid / viz 等大依赖（前端已是按需分包） |
| Windows 上 SmartScreen、macOS Gatekeeper | 与换壳无关；代码签名和公证单独决定 |
| 迁移期间两套壳并存的维护成本 | 前端不分叉（同一个 `Backend` 接口），Go 不分叉（同一个 `internal/app`），只有壳是两份；阶段 5 尽快收敛 |

## 7. 估算

按代码规模估（不是实测）：阶段 0 约 4–6 人天（已完成），阶段 1 约 6–9，阶段 2 约 6–9，阶段 3 约 6–10，阶段 4 约 6–10，阶段 5 约 2–3；合计约 30–47 人天。阶段 6 另行评估。
