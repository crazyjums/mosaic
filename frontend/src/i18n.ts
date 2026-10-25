// 极简的界面多语言：以中文原文作为 key，英文从 EN 表里查，查不到就显示中文。
//
// 为什么不用 i18next 之类的库：界面只有中英两种语言、没有复数和日期格式的需求，
// 一个查表函数就够了；用中文原文当 key，代码读起来和不做多语言时一样直观。
// 语言切换时 App 用 key 重新挂载界面（见 App.tsx），所以 t() 可以是普通函数而不是 hook。
//
// 覆盖范围：聊天界面、状态栏、弹窗，设置页的导航，以及"外观 / 权限与工具 / MCP"页和其他页里新增的设置项。
// 较早的设置页（模型厂商、系统提示词、数据等）里的说明文字暂时只有中文。
// i18n.test.ts 会检查这些文件里每个 t("…") 都有英文翻译。

export type Lang = "zh" | "en";

let current: Lang = "zh";

export function setLanguage(l: Lang) {
  current = l;
}

export function language(): Lang {
  return current;
}

/** 翻译。vars 替换文本中的 {name} 占位符。 */
export function t(zh: string, vars?: Record<string, string | number>): string {
  let s = current === "en" ? (EN[zh] ?? zh) : zh;
  if (vars) s = s.replace(/\{(\w+)\}/g, (_, k: string) => String(vars[k] ?? ""));
  return s;
}

export const EN: Record<string, string> = {
  // ---- App / 侧边栏 ----
  "新会话": "New session",
  "演示模式": "Demo",
  "＋ 新会话": "＋ New session",
  "历史会话": "History",
  "还没有保存的会话": "No saved sessions yet",
  "（无标题）": "(untitled)",
  "删除": "Delete",
  "删除会话「{title}」？": "Delete session \"{title}\"?",
  "运行中不能修改设置": "Settings are locked while the agent is running",
  "⚙ 设置": "⚙ Settings",
  "关闭": "Close",

  // ---- 消息流 ----
  "完成": "completed",
  "达到最大步数": "max steps reached",
  "已取消": "cancelled",
  "出错": "error",
  "{reason} · {steps} 步 · 入 {input} / 出 {output} tokens": "{reason} · {steps} steps · in {input} / out {output} tokens",
  "自己实现的 agent harness · 思考、工具调用、权限确认全过程可视化": "A hand-built agent harness · thinking, tool calls and permission prompts, all visible",
  "当前是浏览器演示模式：没有连接 Go 后端，回放的是 fake provider 录制的事件流。随便输入一句话试试；在 Wails 桌面端中会连接真实内核。":
    "This is the browser demo: there is no Go backend, events recorded from the fake provider are replayed. Type anything to try it; the Wails desktop app connects to the real engine.",
  "当前使用的是演示模型（按固定剧本回复，不调用真实模型）。": "You are using the demo model (scripted replies, no real model is called).",
  "配置真实模型（Base URL + API Key）": "Configure a real model (Base URL + API Key)",
  "等待模型响应": "Waiting for the model",
  "输出达到 max_tokens 上限，被截断。": "Output hit the max_tokens limit and was truncated.",
  "厂商拒绝了这次请求。": "The provider refused this request.",
  "思考中…": "Thinking…",
  "思考过程": "Thinking",
  "{n} 字": "{n} chars",
  "（厂商返回了加密的思考内容，无法显示）": "(the provider returned encrypted thinking, which cannot be shown)",

  // ---- 工具卡片 ----
  "排队中": "queued",
  "等待确认": "awaiting approval",
  "执行中": "running",
  "失败": "failed",
  "已拒绝": "denied",
  "只读": "read",
  "写入": "write",
  "执行": "exec",
  "权限：{summary}": "Permission: {summary}",
  "参数": "Input",
  "结果": "Result",

  // ---- 权限弹窗 ----
  "将修改工作区中的文件": "will modify files in the workspace",
  "将在你的电脑上执行命令": "will run a command on your computer",
  "需要你的确认": "Approval needed",
  "工具": "Tool",
  "拒绝": "Deny",
  "本会话总是允许": "Always allow in this session",
  "允许": "Allow",

  // ---- 输入框 ----
  "等待你确认工具调用…": "Waiting for you to approve a tool call…",
  "agent 正在工作…": "The agent is working…",
  "输入消息，Enter 发送，Shift+Enter 换行": "Type a message · Enter to send, Shift+Enter for a new line",
  "输入消息，{key}+Enter 发送，Enter 换行": "Type a message · {key}+Enter to send, Enter for a new line",
  "停止": "Stop",
  "发送": "Send",

  // ---- 状态栏 ----
  "自定义状态栏": "Customize status bar",
  "切换厂商与模型（下一条消息生效，会被记住）": "Switch provider and model (applies to the next message, remembered)",
  "（不可用：{error}）": " (unavailable: {error})",
  "演示模型 · 配置真实模型 →": "Demo model · configure a real one →",
  "本会话累计：输入 {input} / 输出 {output} tokens": "Session total: {input} in / {output} out tokens",
  "，缓存命中 {n}": ", {n} from cache",
  " · 缓存 {n}": " · cache {n}",
  "上下文": "Context",
  "上下文 {n}": "Context {n}",
  "最近一次请求的上下文大小。在 设置 → 模型厂商 → 高级选项 中填写该模型的上下文窗口，可以显示占比。":
    "Context size of the latest request. Set the model's context window in Settings → Providers → Advanced to show a percentage.",
  "最近一次请求的上下文：{tokens} / {window} tokens（{pct}%）": "Latest request context: {tokens} / {window} tokens ({pct}%)",
  "预估花费（部分调用的模型没有配置单价，或基于会话累计用量近似计算）": "Estimated cost (some models have no price configured, or it is approximated from session totals)",
  "按模型单价估算的本会话花费": "Estimated cost of this session from model prices",
  "本轮已用时间 / 当前是第几次模型调用": "Elapsed time / current model call in this turn",
  " · 第 {n} 步": " · step {n}",
  "上一轮的总耗时": "Duration of the last turn",
  "⏱ 上一轮 {d}": "⏱ last turn {d}",
  "工作区：{path}": "Workspace: {path}",
  "分支 {branch}，{n} 个文件有未提交的改动": "Branch {branch}, {n} files with uncommitted changes",
  "分支 {branch}，没有未提交的改动": "Branch {branch}, no uncommitted changes",
  "运行中": "Running",
  "空闲": "Idle",
  "状态栏脚本出错：{error}": "Status line script failed: {error}",

  // ---- 权限档位 ----
  "默认": "Default",
  "自动接受编辑": "Accept edits",
  "全部放行": "Allow all",
  "只能读文件和列目录，写文件、执行命令一律拒绝": "Only reading files and listing directories; writes and commands are denied",
  "读操作直接放行，写文件和执行命令前询问": "Reads are allowed; ask before writing files or running commands",
  "读写文件直接放行，执行命令前仍询问": "Reads and writes are allowed; still ask before running commands",
  "所有操作都不再询问，包括执行任意命令": "Never ask, including for arbitrary commands",
  "权限：{label}": "Permission: {label}",
  "权限：{desc}（立即生效，会被记住；全部放行除外）": "Permission: {desc} (applies immediately and is remembered, except Allow all)",
  "权限档位": "Permission mode",
  "开启「全部放行」？": "Turn on \"Allow all\"?",
  "开启后，agent 写文件、执行任意 shell 命令都不再经过你的确认。模型可能误删文件、改坏代码，或运行你没有预料到的命令。":
    "The agent will write files and run any shell command without asking you. The model may delete files, break code, or run commands you did not expect.",
  "只建议在可以随意破坏的目录（一次性的沙箱、已提交的 git 仓库）里使用": "Only use it in directories you can afford to break (a throwaway sandbox, a committed git repo)",
  "随时可以在状态栏切回「默认」，立即生效": "You can switch back to \"Default\" in the status bar at any time",
  "只对本次运行有效，不会被记住；重启应用后回到切换前的档位": "It only lasts for this run and is not remembered",
  "取消": "Cancel",
  "我了解风险，全部放行": "I understand, allow all",

  // ---- 设置：导航 ----
  "常规": "General",
  "模型厂商": "Providers",
  "系统提示词": "System prompt",
  "权限与工具": "Permissions & tools",
  "外观": "Appearance",
  "状态栏": "Status bar",
  "数据": "Data",
  "关闭（Esc）": "Close (Esc)",
  "加载中…": "Loading…",
  "保存": "Save",
  "保存中…": "Saving…",
  "修改会立即保存": "Changes are saved immediately",
  "恢复默认": "Reset to defaults",

  // ---- 设置：外观 ----
  "主题": "Theme",
  "跟随系统": "System",
  "浅色": "Light",
  "深色": "Dark",
  "字体大小": "Font size",
  "小": "Small",
  "中": "Medium",
  "大": "Large",
  "整个界面按比例缩放。": "Scales the whole interface.",
  "消息区宽度": "Message width",
  "窄": "Narrow",
  "宽": "Wide",
  "铺满": "Full",
  "思考过程默认": "Thinking cards",
  "自动（输出时展开，结束后折叠）": "Auto (open while streaming, then collapse)",
  "总是展开": "Always expanded",
  "总是折叠": "Always collapsed",
  "发送快捷键": "Send shortcut",
  "Enter 发送，Shift+Enter 换行": "Enter sends, Shift+Enter adds a line",
  "{key}+Enter 发送，Enter 换行": "{key}+Enter sends, Enter adds a line",
  "界面语言": "Language",
  "跟随系统（{lang}）": "System ({lang})",
  "适合经常输入多行内容（代码、列表）的情况。": "Useful if you often type multi-line text (code, lists).",
  "恢复外观的默认设置？": "Reset appearance to defaults?",

  // ---- 设置：常规（新增项） ----
  "会话标题": "Session titles",
  "截取第一句": "First message",
  "由模型生成": "Generated by the model",
  "“由模型生成”会在第一轮对话结束后，用当前模型额外发一次很短的请求（几百个 token）。失败时保留截取的第一句。":
    "\"Generated by the model\" sends one extra short request (a few hundred tokens) with the current model after the first turn. On failure the first message is kept.",
  "API Key 保存到系统钥匙串": "Store API keys in the system keychain",
  "开启后，厂商的 API Key 保存在 macOS 钥匙串 / Windows 凭据管理器 / Linux Secret Service 中，settings.json 里只留引用。立即生效，已有的密钥会被迁移。":
    "API keys are kept in the macOS Keychain / Windows Credential Manager / Linux Secret Service, and settings.json only keeps a reference. Takes effect immediately and migrates existing keys.",
  "不可用：{error}": "Unavailable: {error}",
  "已开启": "On",
  "已关闭": "Off",

  // ---- 设置：权限与工具 ----
  "权限规则": "Permission rules",
  "规则优先于状态栏上的权限档位，从上到下第一条匹配的生效。工具名和匹配内容都支持 * 通配；匹配内容对 shell 是命令，对文件工具是路径，留空匹配任意调用。":
    "Rules take precedence over the permission mode in the status bar; the first matching rule from the top wins. Tool and pattern both support * wildcards; the pattern matches the command for shell and the path for file tools, empty matches any call.",
  "还没有规则。": "No rules yet.",
  "工具名，如 shell": "Tool, e.g. shell",
  "匹配内容，如 go test*": "Pattern, e.g. go test*",
  "询问": "Ask",
  "上移": "Move up",
  "下移": "Move down",
  "＋ 添加规则": "＋ Add rule",
  "保存规则": "Save rules",
  "✓ 已保存，立即生效": "✓ Saved, effective immediately",
  "示例：": "Examples:",
  "工具开关": "Tools",
  "关掉的工具不会发给模型，也就不会被调用。MCP 工具连接成功后也会出现在这里。":
    "Disabled tools are not offered to the model, so they will never be called. Connected MCP tools appear here too.",
  "风险：{risk}": "Risk: {risk}",

  // ---- 设置：MCP ----
  "MCP 服务器": "MCP servers",
  "MCP（Model Context Protocol）服务器为模型提供额外的工具，例如访问 GitHub、数据库、浏览器。工具名会加上 mcp__名称__ 前缀，调用时与内置工具一样经过权限确认。":
    "MCP (Model Context Protocol) servers give the model extra tools, e.g. GitHub, databases or a browser. Tool names get an mcp__name__ prefix and go through the same permission checks as built-in tools.",
  "还没有添加 MCP 服务器。": "No MCP servers yet.",
  "＋ 添加服务器": "＋ Add server",
  "名称": "Name",
  "传输方式": "Transport",
  "本地命令（stdio）": "Local command (stdio)",
  "远程地址（HTTP）": "Remote URL (HTTP)",
  "命令": "Command",
  "参数（每行一个）": "Arguments (one per line)",
  "环境变量（每行 KEY=VALUE）": "Environment (KEY=VALUE per line)",
  "地址": "URL",
  "请求头（每行 Name: Value）": "Headers (Name: Value per line)",
  "工具的风险等级": "Tool risk level",
  "执行（每次询问）": "exec (always ask)",
  "写入（默认档位下询问）": "write (ask in Default mode)",
  "只读（默认档位下直接放行）": "read (allowed in Default mode)",
  "MCP 协议没有可信的风险声明，默认按“执行”处理。只有确认服务器的所有工具都是只读的，才改成只读。":
    "MCP has no trustworthy risk declaration, so tools are treated as exec by default. Only lower it if every tool of the server is read-only.",
  "停用（保留配置但不连接）": "Disabled (keep the config but don't connect)",
  "连接中…": "Connecting…",
  "已连接 · {n} 个工具": "Connected · {n} tools",
  "连接失败": "Failed",
  "已停用": "Disabled",
  "重新连接": "Reconnect",
  "保存并连接": "Save & connect",
  "✓ 已保存，正在后台连接": "✓ Saved, connecting in the background",
  "删除服务器「{name}」？": "Delete server \"{name}\"?",
  "环境变量和请求头保存在 settings.json 中（仅当前用户可读）。": "Environment variables and headers are stored in settings.json (readable only by you).",
  "未保存的修改": "Unsaved changes",
  "示例": "Example",

  // ---- 设置：ChatGPT 账号 ----
  "ChatGPT 账号": "ChatGPT account",
  "用 ChatGPT 账号登录": "Sign in with ChatGPT",
  "使用其他账号": "Use another account",
  "退出登录": "Sign out",
  "未登录": "Not signed in",
  "已登录 {email}": "Signed in as {email}",
  "等待你在浏览器中完成登录…": "Waiting for you to finish signing in in the browser…",
  "浏览器没有自动打开？复制下面的地址，在这台电脑的浏览器中打开：": "Browser didn't open? Copy this address and open it in a browser on this computer:",
  "复制": "Copy",
  "已复制": "Copied",
  "退出 ChatGPT 账号？本机保存的令牌会被删除并吊销。": "Sign out of ChatGPT? The tokens stored on this computer will be deleted and revoked.",
  "使用 OpenAI 官方的“Sign in with ChatGPT”：在浏览器中登录并授权后，可以用 ChatGPT 套餐（Go / Plus / Pro 等）里的额度调用 GPT 模型，不需要 API Key。用量计入你的 ChatGPT 额度，可以在 ChatGPT 的设置中为本应用设置上限或撤销授权。":
    "Uses OpenAI's official \"Sign in with ChatGPT\": after you sign in and authorize in the browser, GPT models run on your ChatGPT plan (Go / Plus / Pro, …) without an API key. Usage counts against your ChatGPT limits; you can cap it or revoke access for this app in ChatGPT settings.",
  "令牌只保存在本机的数据目录中，不会发给界面或其他程序。下面的模型列表可以编辑；额度用完时会提示错误，不会自动改用付费 API。":
    "Tokens are stored only in this computer's data directory and are never sent to the UI or other programs. The model list below is editable; when your plan limit is reached you get an error, it never switches to paid API usage.",

  // ---- 设置：状态栏脚本 ----
  "自定义脚本": "Custom script",
  "脚本输出": "Script output",
  "运行一条命令，把它输出的第一行显示在状态栏上（支持 ANSI 颜色）。当前状态以 JSON 写到命令的标准输入，格式与 Claude Code 的 statusLine 相同。":
    "Runs a command and shows the first line of its output in the status bar (ANSI colors supported). The current state is written to its stdin as JSON, the same format as Claude Code's statusLine.",
  "试运行": "Try it",
  "输出：": "Output: ",
};
