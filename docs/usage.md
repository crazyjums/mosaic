# 使用文档

## 1. 环境要求

| 用途 | 需要 |
| --- | --- |
| 内核、CLI、测试 | Go ≥ 1.26（本机更旧时，默认的 `GOTOOLCHAIN=auto` 会自动下载） |
| 前端 | Node.js ≥ 20.19 或 ≥ 22.12（Vite 8 的要求），npm |
| 桌面端 | 以上全部 + Wails CLI v2.17 + 平台依赖（见下） |

### 安装 Wails CLI 与平台依赖

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
wails doctor   # 检查依赖，缺什么会列出来
```

| 平台 | 依赖 |
| --- | --- |
| macOS | Xcode 命令行工具：`xcode-select --install` |
| Windows | WebView2 运行时（Windows 10/11 通常已自带） |
| Linux (Debian/Ubuntu 22.04) | `sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev` |
| Linux (Ubuntu 24.04+ / Debian 13+) | `sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev`，并在 wails 命令后加 `-tags webkit2_41` |

## 1.5 设置一览

桌面端左下角 **⚙ 设置** 打开设置窗口，共八个标签页。所有修改都保存在 `{data_dir}/settings.json`，不会改动你的 `config.toml`。

| 标签页 | 能设置什么 | 何时生效 |
| --- | --- | --- |
| 常规 | 工作区（可以选择文件夹）、最大步数、单次输出上限、命令超时、会话标题（截取第一句 / 由模型生成）；留空表示使用默认值 | 点保存后，从下一轮对话开始 |
| 常规 → API Key 保存到系统钥匙串 | 开关；钥匙串不可用时置灰并说明原因 | 立即（马上迁移已有密钥） |
| 模型厂商 | 添加/编辑厂商（Base URL、API Key、模型列表）、测试连接、思考/Effort/拒绝回退、每个模型的上下文窗口与单价 | 点保存后立即 |
| 系统提示词 | 使用内置默认（随工作区自动更新）或自定义 | 点保存后，从下一轮对话开始 |
| 权限与工具 | 细粒度权限规则（按工具名 + 命令/路径匹配，允许/询问/拒绝）；每个工具的开关（含 MCP 工具） | 规则点保存后立即；开关立即 |
| MCP | 添加/编辑/删除 MCP 服务器（本地命令 stdio 或远程 HTTP），查看连接状态和提供的工具，重新连接 | 点"保存并连接"后在后台连接 |
| 外观 | 主题（跟随系统/浅色/深色）、字体大小、消息区宽度、思考卡片默认展开方式、发送快捷键（Enter 或 Ctrl/⌘+Enter）、界面语言（中/英） | 修改即保存 |
| 状态栏 | 显示哪些项、顺序、显示方式；自定义脚本 | 修改即保存（脚本点保存） |
| 数据 | 查看数据目录、设置文件、会话数据库的位置和大小；在文件管理器中打开；清空全部历史会话 | 立即 |

**优先级**：命令行参数（CLI 的 `-workspace`、`-mode`）> 界面设置 > `config.toml` > 内置默认。用命令行指定了工作区时，设置页里的工作区会显示为不可修改。界面上保存的工作区如果之后被删除，启动时会回落到默认工作区并在设置页提示。

agent 运行中（一轮还没结束）不能修改常规设置、系统提示词，也不能清空历史，相关按钮会置灰。切换工作区会清除"本会话总是允许"的授权。

## 2. 配置厂商

### 方式一：在界面上配置（推荐）

桌面端左下角 **⚙ 设置 → 模型厂商**（或状态栏上的"演示模型 · 配置真实模型 →"）：

1. **快速选择**一个预设（OpenAI、Anthropic、DeepSeek、通义千问、Moonshot、智谱、硅基流动、OpenRouter、Ollama），或选"自定义（OpenAI 兼容）"；
2. 填 **Base URL** 和 **API Key**；
3. 点 **从服务器获取模型列表**，点选需要的模型（也可以直接输入模型名回车添加）；
4. 点 **测试连接**：用默认模型发一条极短的消息，验证地址、密钥、模型名都正确（会产生极少量费用）；
5. **保存**。立即生效，不需要重启；如果当前还在用演示模型，会自动切换过去。

之后在输入框下方的下拉框里切换厂商和模型，选择会被记住，下次启动直接使用。

界面上的配置保存在 `{data_dir}/settings.json`（默认 `~/.myllmapp/settings.json`，权限 600），**API Key 以明文保存在这个文件里**，请不要把它拷进仓库或分享出去。界面上只显示密钥的掩码（例如 `sk-…a1b2`），编辑时密钥留空表示不修改。

### 方式二：用 ChatGPT 账号登录（使用套餐额度）

**设置 → 模型厂商 → 用 ChatGPT 账号登录**（左下角按钮）：

1. 点 **用 ChatGPT 账号登录**，系统浏览器会打开 OpenAI 的登录页（没有自动打开时，界面上有地址可以复制，必须在**这台电脑**的浏览器里打开）；
2. 登录 ChatGPT，确认授权 myllmapp 使用你的套餐额度；
3. 浏览器显示"登录成功"后回到应用：模型厂商里多了一个 **chatgpt**，模型列表会自动从服务器获取；如果当前还在用演示模型，会自动切换过去。

之后像其他厂商一样在状态栏的下拉框里选择 `chatgpt / <模型>`。CLI 里用 `/login`（换账号用 `/login new`）和 `/logout`，登录信息与桌面端共用。

- 用的是 OpenAI 官方为开源应用提供的 **Sign in with ChatGPT**（动态注册 + OAuth），请求走公开的 Responses API，消耗的是 ChatGPT 套餐（Go / Plus / Pro 等）的额度，**不需要 API Key**，也不会扣 API 余额；
- 额度用完时会提示"套餐额度已用完"，不会自动改用付费 API；可以在 ChatGPT 的设置里为本应用设置用量上限或撤销授权；
- 令牌保存在 `{data_dir}/chatgpt/credential.json`（权限 600），访问令牌 1 小时过期、自动刷新；刷新令牌 30 天有效，过期后需要重新登录。**退出登录**会吊销并删除令牌；
- 按照条款，令牌只在本机使用，不会发给界面、其他程序或其他人；
- 套餐额度这条路有一些限制：不能设置单次输出上限（"常规 → 单次输出上限"对它不生效），不支持服务端托管的 MCP 等功能；高级选项里可以关闭思考摘要或调整思考深度；
- 没有提供"设备码"登录：远程服务器上的 CLI 无法完成登录（回调只监听本机 127.0.0.1）。

### 方式三：配置文件

适合手写、版本管理或团队共享（密钥放环境变量）。

不写任何配置也能运行：内置默认配置包含 fake（离线演示）以及 anthropic / openai / deepseek 三个配置档（从环境变量读密钥）。

需要自定义时：

```bash
cp config.example.toml config.toml      # 或 ~/.myllmapp/config.toml
export ANTHROPIC_API_KEY=...            # 密钥放在环境变量里
export DEEPSEEK_API_KEY=...
```

配置文件查找顺序：命令行 `-config` > 环境变量 `MYLLMAPP_CONFIG` > `./config.toml` > `~/.myllmapp/config.toml` > 内置默认。

每个字段的说明见 [`config.example.toml`](../config.example.toml)。要点：

- `[[providers]]` 的 `type` 是适配器（`openai` / `anthropic` / `fake`），`name` 是你起的名字。所有 OpenAI 兼容厂商都用 `type = "openai"` + 各自的 `base_url`。
- 密钥：`api_key_env` 指向环境变量（推荐）；`api_key` 直接写在文件里（不推荐）。两者都有时环境变量优先。
- **不要提交 `config.toml`**（已在 `.gitignore` 中）。仓库里有 `internal/archtest` 测试会扫描疑似密钥。
- 缺少密钥的配置档不会导致启动失败，只是在界面上显示为不可用并给出原因。
- 界面上可以编辑配置文件里的厂商：保存后会在 settings.json 里生成一份"覆盖"，config.toml 本身不会被改写（程序永远不会修改你手写的配置文件）。在界面上"恢复配置文件设置"即可删除覆盖。

数据（会话数据库）默认在 `~/.myllmapp/sessions.db`，可用 `data_dir` 修改。

工作区：CLI 默认是运行命令时所在的目录；桌面端在 `workspace` 留空或为 `"."` 时使用 `~/.myllmapp/workspace`（启动时自动创建）。从 Finder / 开始菜单启动时"当前目录"通常是 `/` 或安装目录，不适合作为工作区。想让桌面端操作某个项目，请在配置文件里写它的绝对路径。

## 2.5 自定义状态栏

输入框下方的状态栏可以选择显示哪些项、调整顺序：点状态栏最右侧的 **⚙**，或 **设置 → 状态栏**。修改立即生效并保存（`settings.json` 的 `statusline` 字段）。

| 项目 | 显示内容 |
| --- | --- |
| 厂商 / 模型 | 下拉框，直接切换（会被记住） |
| Token 用量 | 本会话累计 ↑输入 ↓输出，可选显示缓存命中；每次模型调用后实时更新 |
| 上下文占用 | 最近一次请求的真实上下文大小；配置了模型的上下文窗口时显示进度条和比例（≥60% 变黄，≥85% 变红） |
| 预估花费 | 按每个模型的单价逐条计算；带 `+` 表示有调用没有单价或是近似值 |
| 本轮耗时 / 步数 | 运行中实时计时、显示第几步；空闲时显示上一轮耗时 |
| 工作区 | 目录名或完整路径 |
| Git 分支 | 分支名，`●N` 表示 N 个文件有未提交改动；每轮结束和每 15 秒刷新 |
| 权限模式 | 下拉选择 只读 / 默认 / 自动接受编辑 / 全部放行，立即生效并被记住；选「全部放行」会先弹出危险确认，且不会被记住（重启后回到之前的档位） |
| 运行状态 | 空闲 / 运行中 / 等待确认 |

| 脚本输出 | 自定义命令输出的第一行，支持 ANSI 颜色（见下文） |

某一项暂时没有数据时会自动隐藏（例如还没发消息时不显示上下文）。

**自定义脚本**：在 **设置 → 状态栏 → 自定义脚本** 填一条命令（macOS/Linux 用 `sh -c` 执行，Windows 用 `cmd /C`），
它输出的第一行会显示在状态栏上。当前状态以 JSON 写到命令的标准输入，约定与 Claude Code 的 `statusLine` 相同，现成的脚本可以直接用：

```json
{"session_id": "...", "provider": "openai", "model": "gpt-...", "input_tokens": 1234, "output_tokens": 567,
 "workspace": "/path", "extra": {"title": "...", "running": false, "permission_mode": "default",
 "context_tokens": 2048, "context_window": 200000, "cost_usd": 0.012, "git_branch": "main"}}
```

命令在工作区目录下运行，超时 2 秒，会话/模型/用量变化时（最多每秒一次）和每 15 秒刷新一次。出错时状态栏显示红色的 `⚠ script`，鼠标悬停可以看到原因。

## 2.6 权限规则

状态栏上的权限档位是"一刀切"的；**设置 → 权限与工具 → 权限规则** 可以更细：

| 工具 | 匹配内容 | 动作 | 效果 |
| --- | --- | --- | --- |
| `shell` | `go test*` | 允许 | 跑测试不再询问 |
| `shell` | `rm *` | 拒绝 | 删除命令一律拒绝（即使在"全部放行"下） |
| `write_file` | `docs/*` | 允许 | 写文档目录不询问 |
| `*` | `*.env` | 拒绝 | 任何工具都不能碰 .env |
| `mcp__github__*` | （空） | 询问 | GitHub MCP 的所有工具每次都询问 |

- 从上到下第一条匹配的规则生效；都不匹配时按状态栏的档位处理。规则优先于档位：`拒绝` 在"全部放行"下也生效，`允许` 在"只读"下也生效。
- 匹配内容对 `shell` 是命令，对文件工具是路径，对其他工具依次取 `command` / `path` / `url` 参数；`*` 匹配任意字符（包括 `/`），`?` 匹配一个字符；留空匹配任意调用。
- **工具开关**：关掉的工具不会发给模型。例如不希望 agent 执行任何命令，可以直接关掉 `shell`。

## 2.7 MCP 服务器

**设置 → MCP → 添加服务器**：

- **本地命令（stdio）**：例如官方的文件系统服务器，命令 `npx`，参数 `-y`、`@modelcontextprotocol/server-filesystem`、`/允许访问的目录`（每行一个）。需要令牌的服务器把令牌写在"环境变量"里（`GITHUB_TOKEN=...`）。
- **远程地址（HTTP）**：填 Streamable HTTP 地址，认证写在"请求头"里（`Authorization: Bearer ...`）。

保存后在后台连接，列表左侧的圆点表示状态（黄：连接中，绿：已连接，红：失败）。连接失败时会显示原因，本地命令还会附上它在 stderr 输出的最后几行。
连接成功后，工具以 `mcp__名称__工具名` 出现在 **权限与工具** 的工具列表里，模型可以调用；调用时与内置工具一样经过权限确认——MCP 工具默认按"执行"风险处理，每次都会询问，可以用权限规则放行。

MCP 的环境变量和请求头保存在 `settings.json` 中（权限 0600），不会放进系统钥匙串。

## 2.8 外观与语言

**设置 → 外观** 的所有修改立即生效：主题、皮肤（见下文）、字体大小（整个界面按比例缩放）、消息区宽度、思考卡片默认展开方式（自动 = 输出时展开、结束后折叠）、发送快捷键、界面语言。
界面语言"跟随系统"时按系统语言选择中文或英文。目前聊天界面、状态栏、弹窗、设置页导航和新增的设置页都有英文；较早的设置页（模型厂商、系统提示词、数据）里的说明文字暂时只有中文。

### 皮肤

**设置 → 外观 → 皮肤**：皮肤是一套配色，分深色和浅色两份；上面的"主题"决定显示哪一份。

- 内置 7 套：默认、海洋、森林、暖阳、玫瑰、Nord、高对比，点一下立即生效；
- **新建皮肤**：以当前皮肤为起点，给 12 个颜色（背景、面板、文字、强调色、强调色上的文字、成功 / 警告 / 危险……）选色或直接输入 `#rrggbb`，
  修改会实时预览；切换"深色 / 浅色"分别编辑两套颜色。保存后出现在列表里，鼠标移到卡片上可以再编辑或删除；
- **导出 / 导入**：编辑器里的"导出"把皮肤复制成 JSON，别人在"导入…"里粘贴即可使用。只缺几个颜色的 JSON 也能导入，缺的用默认皮肤的颜色补上：

```json
{ "name": "我的橙色", "dark": { "accent": "#ff7a1a", "on-accent": "#1a0d00" }, "light": { "accent": "#c2410c" } }
```

颜色只接受 `#rrggbb`；自定义皮肤和其他外观设置一起保存在 `settings.json` 的 `ui` 字段里。"恢复默认"不会删除自定义皮肤。

## 2.9 API Key 保存到系统钥匙串

**设置 → 常规 → API Key 保存到系统钥匙串**：开启后，界面上配置的 API Key 保存在 macOS 钥匙串 / Windows 凭据管理器 / Linux Secret Service（GNOME Keyring、KWallet）中，`settings.json` 里只留 `"api_key_ref": "keychain"`。开关立即生效：开启时把已有密钥迁过去，关闭时迁回文件并删除钥匙串条目。

- 没有 Secret Service 的 Linux（服务器、精简桌面）上开关会置灰，并显示原因；
- 启动时如果钥匙串被锁定、读不出密钥，对应厂商会显示为不可用，设置页给出提示，**引用会保留**，解锁后重启即可；
- 来自 `config.toml` / 环境变量的密钥不受影响（本来就不写进 settings.json）。

**上下文窗口和单价**：在 **设置 → 模型厂商 → 编辑 → 高级选项 → 模型信息** 中按模型填写（单价单位：美元 / 百万 token）。Anthropic 预设已经填好了，其他厂商需要自己填，不填就不显示比例和花费。也可以写在 `config.toml` 里，见 `config.example.toml` 中的 `models_meta`。花费只是估算（缓存命中按输入价计），以厂商账单为准。

## 3. 用 CLI 调试

CLI 和桌面端使用完全相同的内核，是调试 agent 行为最快的方式。

```bash
# 交互模式（默认 fake 演示剧本，不需要密钥）
go run ./cmd/cli

# 一次性演示：第一行是问题，后面两行回答两次权限确认（允许写文件、拒绝执行命令）
make demo
# 等价于
printf '帮我做个演示\ny\nn\n' | go run ./cmd/cli -no-store -workspace "$(mktemp -d)"

# 使用真实厂商
go run ./cmd/cli -provider anthropic -model claude-opus-5-5
go run ./cmd/cli -provider deepseek -p "列出当前目录下的 Go 文件"

# 导出原始事件流（JSONL），排查协议问题最直接的办法
go run ./cmd/cli -json -yes -p "hi" > events.jsonl
```

| 参数 | 说明 |
| --- | --- |
| `-config` | 配置文件路径 |
| `-provider` / `-model` | 覆盖默认厂商配置档和模型 |
| `-workspace` | 工作区目录（工具读写的根目录） |
| `-mode` | 权限模式：`readonly` / `default` / `accept_edits` / `auto`（运行中可用 `/mode` 切换） |
| `-p "..."` | 单次执行后退出 |
| `-yes` | 自动允许所有权限请求（谨慎） |
| `-json` | 以 JSONL 输出原始事件，提示信息走 stderr |
| `-no-store` | 不写数据库 |
| `-resume <id>` | 恢复会话 |
| `-sessions` | 列出已保存的会话后退出 |

交互模式命令：`/help`、`/new`、`/sessions`、`/load <id>`、`/models`、`/model <profile> [model]`、`/status`、`/quit`。
**Ctrl-C** 取消当前这一轮（空闲时退出），**Ctrl-D** 退出。权限确认时输入 `y` 允许一次、`a` 本会话总是允许、`n` 拒绝。

## 4. 前端单独开发（浏览器演示模式）

```bash
cd frontend
npm install
npm run dev        # http://localhost:5173
```

浏览器里没有 Go 后端，前端会自动切换到**演示模式**（左上角有标记），回放 fake provider 录制的事件流，权限弹窗可以真实点击。适合调界面样式。

修改了 fake 剧本或事件协议后，重新录制演示数据：

```bash
make demo-events
```

前端检查：

```bash
npm run typecheck   # 类型检查
npm test            # Vitest（reducer 单元测试）
npm run build       # 生产构建
```

## 5. 桌面端开发模式

```bash
wails dev                    # Linux 新发行版：wails dev -tags webkit2_41
```

Wails 会启动 Vite 开发服务器并打开桌面窗口，前端热更新，Go 代码改动后自动重新编译。
开发模式下也可以用浏览器访问 Wails 打印出的地址（通常是 http://localhost:34115），此时调用的是真实 Go 后端。

> 根目录 `main.go` 带有构建标签 `desktop || dev || production || bindings`，Wails CLI 会自动传入，平时的 `go build ./...` / `go test ./...` 会跳过它。

## 6. 打包

推荐用一键打包脚本 [`scripts/build.sh`](../scripts/build.sh)。它会自动识别当前系统、检查依赖（缺 Wails CLI 会自动安装；Linux 上自动判断 WebKitGTK 4.0/4.1 并加对应构建标签）、跑测试、编译：

```bash
./scripts/build.sh               # 编译本系统版本：macOS → .app（universal），Linux → 可执行文件，Windows → .exe
./scripts/build.sh --windows     # 在 macOS / Linux 上交叉编译 Windows 版 .exe
./scripts/build.sh --all         # 本系统版本 + Windows 版本
./scripts/build.sh --windows --nsis   # 额外生成 Windows 安装包（需要安装 NSIS：brew install nsis / apt install nsis）
./scripts/build.sh -h            # 全部参数
```

| 在哪个系统上运行 | 能编译出 |
| --- | --- |
| macOS | macOS `.app`、Windows `.exe` |
| Linux | Linux 可执行文件、Windows `.exe` |
| Windows（Git Bash / MSYS2） | Windows `.exe` |

macOS 和 Linux 版本依赖本机的 CGO 与系统 WebView，只能在对应系统上编译；Windows 版不需要 CGO，所以可以交叉编译。

也可以直接用 Wails 命令：

```bash
wails build                  # 产物在 build/bin/
wails build -platform darwin/universal
wails build -platform windows/amd64 -nsis   # Windows 安装包（需要 NSIS）
```

`build/appicon.png` 是应用图标，`build/windows/` 是 Windows 的图标、版本信息和 manifest，改完重新打包即可生效。macOS 的 Info.plist 在第一次于 macOS 上打包时生成到 `build/darwin/`。

> 前端资源只在 production 构建中嵌入（`assets_production.go`）。这是因为 Wails 先生成绑定、后构建前端，生成绑定时 `frontend/dist` 还不存在。

## 7. 一键检查

```bash
make check     # go vet + go test + 前端类型检查/测试/构建
```

## 8. 常见问题

| 现象 | 原因 / 处理 |
| --- | --- |
| Windows 上偶尔有黑色窗口闪一下（例如切换权限、每隔一段时间） | 旧版本启动 git、状态栏脚本等命令行程序时没有禁止创建控制台窗口，已修复（`internal/sysproc`）；请更新到最新版本重新打包 |
| `go: downloading go1.26...` | 本机 Go 低于 1.26，自动下载工具链，正常现象 |
| `pattern all:frontend/dist: no matching files found` | 手动 `go build -tags desktop,production` 时前端还没构建。用 `wails build` / `scripts/build.sh`，或先 `cd frontend && npm run build` |
| Linux 虚拟机 / 远程桌面里窗口全黑 | WebKit 的硬件合成不可用，启动前设置 `WEBKIT_DISABLE_COMPOSITING_MODE=1` |
| Linux 上找不到 `webkit2gtk-4.0` | 新发行版只有 4.1，加 `-tags webkit2_41` |
| 界面上厂商显示"不可用：missing API key" | 对应环境变量没有设置，或桌面应用没有继承终端的环境变量（macOS 从 Dock 启动时常见）——可以在 `config.toml` 中用 `api_key` 直接配置，或从终端启动 |
| Anthropic 报 400 与 thinking 相关 | 旧模型或代理不支持 adaptive thinking：配置该配置档 `options.thinking = "omit"` |
| OpenAI 报 `max_tokens` 不支持 | 设置 `options.max_tokens_field = "max_completion_tokens"` |
