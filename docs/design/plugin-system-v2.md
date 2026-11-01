# Mosaic Plugin System V2：从“安装成功”到“真正可用”

状态：设计提案（**尚未实施**）。现有基础见 [plugin-system.md](plugin-system.md) 和 [插件开发指南](../plugin-development.md)。本方案以 2026-11-01 的 master 代码为基准，尽量沿用已有 Go/Wails + React、plugin.Host、mcp.Manager、skill 包和工具权限系统，不重新发明 Agent 内核。

## 1. 背景与可确认的现状

Mosaic 已有：内置/在线插件市场、文件夹/ZIP/Git 安装、安装确认、插件启停、设置表单、系统钥匙串、Skill 注入、斜杠命令、MCP 工具注册、状态脚本、操作按钮和一键授权。这说明问题不在“完全没有插件系统”，而在“安装后是否可用”的链路没有打通和展示。

仓库现有 8 个市场插件，其中 code-review、debug-helper、git-assistant、writing-kit、sql-helper、hello-skill 主要提供 Skill/命令；echo-mcp 真正运行 MCP 服务且需要 python3；feishu 需要 python3 和 lark-cli，真实租户集成尚未全面验证。必须明确区分“增强提示词”与“连接工具”。

### 1.1 代码层面确认的问题（不是用户机器上的最终诊断）

| 编号 | 问题 | 代码依据 | 影响 |
| --- | --- | --- | --- |
| D01 | 已启用与实际可用被混淆 | internal/plugin/host.go 的 Info.Enabled 取持久化状态，PluginSettings.tsx 据此显示绿色点；MCP 的真实 State/Tools 在 internal/mcp/manager.go | 子进程失败仍显示“已启用”，没有工具可用 |
| D02 | 状态脚本与 MCP 握手脱节 | plugin.Host.Status 执行清单 status.command，未聚合 mcp.Manager.Statuses | status 显示 ok 不保证 tools/list 成功 |
| D03 | 依赖只提示，未形成完整配置闭环 | Preview 和 Install 可在缺少 python3/lark-cli 时继续；MCP 异步启动 | 安装成功后用户不知道先做什么 |
| D04 | 缺少统一试用路径 | 安装完成转已安装详情；只有作者自定义 action，缺统一测试工具/命令按钮 | 不知道如何验证插件有没有工作 |
| D05 | 市场刷新不使用应用内全局代理 | internal/plugin/catalog.go RefreshCatalog 使用 http.DefaultClient | 模型联网正常但在线插件列表刷新失败 |
| D06 | 生命周期约束不完整 | plugin.Host.prepareRun/RunAction 和 StartLogin 没有统一检查 pluginState.Enabled | 停用不代表所有插件执行入口都停用 |
| D07 | 缺跨平台端到端验收门禁 | 现有 Go 测试验证安装和 MCP；依赖未安装时用 Skip，前端主要是逻辑单测 | CI 绿不等于 Windows 新用户可完成一次使用 |
| D08 | 无工具级可观测性与错误恢复 | plugin Info 缺运行时健康汇总；MCP Manager 只保留连接状态/错误；详情无统一重连/诊断入口 | 失败时只能猜是授权、依赖、连接还是模型不支持工具 |
| D09 | 市场能力解释不充分 | 内置条目有 6 个仅命令/技能；安装后没有固定的“如何使用”按钮 | 预期以为是工具，实际是提示词模板 |

注：以上是静态代码审查；不能据此断言某位用户机器上的插件失败根因。需要运行时诊断才能最终确认。

## 2. 产品目标与边界

**核心体验**：用户安装一个插件后，必须立刻知道它是什么、是否准备就绪、如何使用；如果不能用，要知道为何不能用并能一键重试/复制脱敏诊断。

1. **安装即引导**：安装确认后进入安装向导，不以“Enabled=true”代表成功。
2. **能力可见**：Skill、命令、工具、连接器分别展示，且每项有明确入口。
3. **验证可重复**：系统自检、工具发现、实际只读测试、对话中调用形成端到端证据。
4. **安全透明**：插件执行程序可能获取用户本机权限；不暗示沙箱已经存在。敏感读取和写入继续由权限系统决定。
5. **兼容旧插件**：当前 plugin.json 协议 1 的全部插件不修改也能加载；新字段/组件逐步引入。
6. **不捆绑在特定模型**：插件工具可用与当前模型是否支持工具调用分别提示，避免把 Provider 的限制混进插件错误。

### 非目标（第一阶段）

- 不开放任意 React/JS 注入到设置页。
- 不自动执行脚本来安装系统软件包，不静默升级第三方代码。
- 不立即支持任意类型的 hooks、subagents 或云端插件托管。
- 不声称实现真实 OS 级容器沙箱；插件本地进程运行权限仍受操作系统账号约束。
- 不把独立 Skill 插件包装成“外部工具”，也不承诺所有插件对所有模型兼容。

## 3. 架构设计

~~~mermaid
flowchart TD
  UI["React 插件中心<br/>市场 / 已安装 / 详情 / 诊断"] --> API["Wails Plugin API"]
  API --> HOST["plugin.Host / Plugin Service<br/>安装、配置、版本、启停、持久状态"]
  HOST --> INST["Installer + Preflight<br/>来源校验、依赖、清单、安装事务"]
  HOST --> SUP["Runtime Supervisor<br/>启动、状态聚合、重试、错误码"]
  HOST --> SKILLS["Skill Registry / read_skill"]
  HOST --> COMMANDS["Slash Command Registry"]
  SUP --> MCP["mcp.Manager / Tool Registry"]
  SUP --> AUTH["Auth & Secrets<br/>钥匙串 / OAuth / 状态"]
  MCP --> POL["Agent 权限策略<br/>按工具审批、敏感数据确认"]
  POL --> MODEL["当前模型 Provider"]
  INST --> STATE["plugins.json + plugin-data"]
  SUP --> LOGS["脱敏诊断事件 + 最近错误"]
  LOGS --> API
~~~

**职责不混用**：

- **Host**：插件文件、声明、是否允许启用、持久化配置。继续使用现有实现，不管理具体模型调用。
- **Preflight/Installer**：安装前可验证的静态条件，阻止格式错误；依赖缺失可选择安装到“需配置”状态，不谎称就绪。
- **Runtime Supervisor（新增薄层）**：汇总一个插件的所有 MCP 连接、所需依赖、认证状态和能力注册结果；监听启停/配置变化，管理失败重试。
- **MCP Manager**：继续负责 MCP stdio/HTTP 连接、握手、tools/list、工具注册和调用；不复制它的逻辑。
- **Skill/Command Registry**：继续负责技能发现/命令展开；必须报告实际注册数量和可用入口。
- **Permission**：继续负责 Agent 发起的工具调用；**插件详情按钮是独立操作通道**，必须单独鉴权/确认危险操作。
- **Observability**：只记操作与状态摘要、错误码、毫秒耗时、插件 ID、服务名和脱敏 stderr；不记录密钥、完整工具参数和私有返回内容。

## 4. 能力分类与可用状态

每个插件展示如下独立信息：

| 属性 | 示例 | 含义 |
| --- | --- | --- |
| installed | true | 文件已安装 |
| enabled | true | 用户允许启用 |
| overall_state | needs_setup | 当前整体是否能完成其声明的用途 |
| capabilities.skills | 2 discovered | Skill 能被模型读取 |
| capabilities.commands | 2 registered | 输入框可用的 / 命令 |
| capabilities.mcp | 1 connected, 3 tools | 真正可调用的 MCP 工具 |
| requirements | python3 missing | 外部依赖 |
| auth | needs_login | 第三方账号认证 |
| tool_compatibility | unsupported_for_current_model | 当前模型是否支持该工具格式 |
| last_check_at | timestamp | 最近诊断时间 |
| last_error | MCP_HANDSHAKE_TIMEOUT | 可行动的错误码 |

推荐状态：

~~~mermaid
stateDiagram-v2
  [*] --> not_installed
  not_installed --> installing: 用户确认
  installing --> needs_setup: 依赖/配置/授权缺失
  installing --> starting: 无阻塞项
  needs_setup --> starting: 用户完成配置
  starting --> ready: 所需能力验证通过
  starting --> degraded: 部分能力可用
  starting --> failed: 无关键能力可用
  failed --> starting: 用户重试/自动退避
  degraded --> ready: 恢复
  ready --> degraded: 工具掉线/授权失效
  ready --> disabled: 用户停用
  failed --> disabled: 用户停用
  disabled --> starting: 用户启用
  needs_setup --> disabled: 用户停用
  disabled --> [*]: 卸载
~~~

**重要语义**：

- 对“只有 Skill”的插件：不要求 MCP 连接；发现 Skill 且无配置缺失，可判为 ready，并显示“通过 /review 或自动触发使用”。
- 对“只有 MCP”的插件：需要握手、tools/list、至少发现一项预期工具；连接中与已安装不是 ready。
- 对“组合插件”：部分技能正常、MCP 失败时标注 degraded，展示能用与不能用的能力，不能笼统说全部不可用。
- enabled 是“允许启用”，不再用作绿色 ready 指示。
- 检查插件是否可用不能依赖模型回复“似乎可以”；以工具注册/调用结果和所选模型实际支持为证据。

## 5. 用户体验与页面设计

### 5.1 插件中心

入口：设置 → 插件。顶部保留两个 Tab：**发现插件 / 已安装**，新增健康摘要。

~~~text
插件中心                                   [搜索] [+ 添加插件]
发现插件 | 已安装 (6)

已安装 6   已就绪 3   需要配置 1   运行异常 2

[Git 助手]      就绪 · 2 个命令     [使用 /commit-msg]
[代码评审]      就绪 · 1 个 Skill   [使用 /review]
[回声 MCP]      缺少依赖 python3   [修复]
[飞书]          未登录              [继续设置]
~~~

每张卡片展示“是什么类型 / 怎么用 / 当前能不能用 / 下一步做什么”，不只展示是否安装。

### 5.2 安装向导（五步）

1. **来源预览**：名称、作者/来源 URL、版本、最近更新、组件清单、风险声明。对 Git 来源显示准确 commit SHA。
2. **环境预检**：检查命令真实可执行而不只 LookPath；解释 Windows python3/py、macOS、Linux 的差别。展示本机缺失项以及官方安装说明，不静默安装。
3. **权限确认**：展示需要运行的本机程序、MCP 能力、数据是否会传给模型；只读权限请求与敏感数据标记分开展示。
4. **配置/授权**：声明式配置，必填项验证；凭据进钥匙串；远程 MCP 支持标准 OAuth / API Key 授权方式。
5. **连接并试用**：启动 MCP → 握手 → tools/list → 显示可用工具 → 执行作者提供的安全自测；仅全部就绪才显示“完成”。

用户可以在缺失依赖时选择“先安装稍后配置”，但插件状态必须是 needs_setup，而不是 ready。

### 5.3 插件详情

~~~text
回声 MCP v0.1.0                         [启用] [卸载]
状态：需要修复                           [运行诊断]

无法使用：未检测到可运行的 Python
  1. 需要 python3
  2. [查看安装说明] [重新检测]
  3. 成功后自动尝试连接服务

能力
  Skill       0
  斜杠命令    0
  MCP 服务    echo-mcp  [连接失败] [重连]
  MCP 工具    0 / 2

设置 | 连接与授权 | 工具列表 | 运行日志 | 安全权限
                                              [试用插件]
~~~

**试用入口**：

- Skill/命令：一键将 /review 或 /hello 插入当前会话输入框，用户可以直接发送。
- MCP：展示工具名称、描述和输入 Schema，开发/调试模式可填写参数、按正常审批路径单次调用。不能在“试用”里绕过敏感数据确认。
- 组合插件：提供插件作者声明的示例任务；例如“总结飞书文档”会打开可编辑的示例消息，不自动执行未经用户确认的外部读取。

### 5.4 聊天界面

- 输入框增加紧凑的“能力”入口，能浏览**当前可用** Skill、命令和工具；不把用户已经启用但不可用的服务混入可调用列表。
- 输入 / 时继续兼容已有菜单，同时展示来源图标、用法和当前启用状态。
- 工具调用卡片展示“来自插件：飞书”，点击可打开插件详情的该工具和诊断记录。
- 当当前模型拒绝工具 Schema（HTTP 400 等）时，提示“模型/接口不支持或拒绝了工具定义”，不是错误标成插件未安装。

## 6. 后端接口与数据结构（拟新增）

现有 Wails 绑定如 ListPlugins / PreviewPlugin / InstallPlugin / SetPluginEnabled / GetPluginStatus 继续保留。新增：

| 拟新增接口 | 作用 |
| --- | --- |
| GetPluginDiagnostics(id) | 聚合安装状态、环境依赖、各 MCP 服务状态、注册的 Skill/命令/工具和最近错误 |
| TestPlugin(id, capability, testID) | 根据声明执行不带副作用的安全自测；显式执行其他工具时复用权限审批 |
| ReconnectPlugin(id) | 仅重启该插件声明的 MCP 服务，不影响其他插件及当前模型选择 |
| ListPluginCapabilities(id) | 返回当前真正注册的每项能力和使用说明 |
| GetPluginLogs(id, cursor, limit) | 最近诊断记录，严格限量、脱敏、按时间分页 |
| ExportPluginDiagnostics(id) | 导出不含密钥/私密内容的诊断报告 |
| ResolvePluginRequirements(id) | 执行依赖探针、返回安装说明；默认不安装系统包 |

推荐 Go 结构（字段示意，非已实现代码）：

~~~go
type PluginDiagnostics struct {
    ID            string             `json:"id"`
    Installed     bool               `json:"installed"`
    Enabled       bool               `json:"enabled"`
    State         string             `json:"state"`
    Requirements  []RequirementCheck `json:"requirements"`
    MCP           []MCPHealth        `json:"mcp"`
    SkillCount    int                `json:"skill_count"`
    CommandCount  int                `json:"command_count"`
    ToolCount     int                `json:"tool_count"`
    LastErrorCode string             `json:"last_error_code,omitempty"`
    LastError     string             `json:"last_error,omitempty"`
}
~~~

诊断错误必须有机器可识别代码：PLUGIN_INVALID_MANIFEST、PLUGIN_INCOMPATIBLE、DEPENDENCY_NOT_FOUND、DEPENDENCY_NOT_RUNNABLE、CONFIG_REQUIRED、AUTH_REQUIRED、MCP_CONNECT_TIMEOUT、MCP_HANDSHAKE_FAILED、MCP_TOOL_LIST_EMPTY、MCP_PROCESS_EXITED、TOOL_REGISTRATION_CONFLICT、MODEL_TOOL_UNSUPPORTED、CATALOG_NETWORK_FAILED。每个错误附带“用户下一步操作”，而不只提供 stderr 字符串。

## 7. 插件协议与兼容性

**先兼容旧插件，不急于修改全部清单。** 当前 plugin.json 的 protocol=1 不变；插件内核提供 v1 → 内部 CapabilitySpec 的适配器。后续引入可选 protocol=2，用于声明试用、环境探针和更完整的运行时需求；再根据实际需求实现导入 Codex/Claude Code 插件常见目录结构。

下一版新增的概念：

- capabilities：明确区分 skills、commands、mcp，不以 mcp 作为所有插件的必填。
- requirements：支持按 OS 声明多个可用命令候选和可执行探针，而不止 LookPath。
- onboarding：声明一个安全的测试步骤或示例消息；默认不允许任意脚本作为“无确认自测”执行。
- permissions：区分 MCP 工具审批、第三方敏感数据传输、本地插件 action；不让声明的 risk 绕过实际权限系统。
- compatibility：声明支持的 Mosaic 协议和平台，区分应用不兼容与本机依赖缺失。
- source：记录插件来源、git commit/归档校验和安装时间；未来可做更新前 diff 预览。

对 Codex/Claude Code 生态遵循**可导入，而不是复制实现**：优先识别 Skills / MCP 标准组件；Hooks、用户可执行脚本和其他非支持组件明确提示“需要适配”，而非忽略或默默执行。

## 8. 安全设计

1. **源代码与安装前审查**：插件是用户电脑上执行的外部代码，不承诺沙箱；市场列表提供来源信息但不自动建立信任，安装/更新需要用户确认。
2. **权限分层**：安装信任（允许运行本地插件进程）与 Agent 工具权限（read/write/exec/sensitive）分开。插件的 status/action/login 后端执行入口也必须检查 enabled，且高风险操作单独要求确认。
3. **工具输出不可信**：MCP 返回的文档/消息当作第三方数据，不得从工具输出继承更高权限；敏感操作继续每次确认。
4. **秘密与环境**：密码进钥匙串，仅注入特定服务的进程环境，不出现在诊断日志。保留当前子进程环境白名单。
5. **权限不可升级**：插件元信息申请的 read 不等于获得授权；动态新增工具必须重新遵循风险等级，不使用先前更宽的默认权限。
6. **生命周期**：停用时取消登录/操作/状态脚本，停止所有 MCP 服务，移除工具；在途工具调用给出清晰的取消结果。
7. **日志安全**：聚合错误前清洗 token、Authorization、Cookie、API Key、用户文件正文，不收集原始提示词或文档内容。
8. **网络代理**：市场刷新、Git 下载（按 Git 的代理策略）、远程 MCP 服务分别明确展示有效代理与连接失败原因，不直接凭“浏览器能访问”推断 Go 进程能访问。
9. **不自动安装依赖**：引导用户安装 Python/Node/CLI，并在确认后重新检测。未来若做一键安装，限定官方来源、校验哈希、要求明确授权。
10. **执行资源控制**：健康检查超时、限流和取消；失败使用指数退避 + 随机抖动；进程退出清理注册工具，防止后台反复崩溃重启。

## 9. 生命周期与异常恢复

正常流程：

~~~mermaid
sequenceDiagram
  participant U as 用户
  participant UI as React 插件中心
  participant H as Plugin Host
  participant R as Runtime Supervisor
  participant M as MCP Manager
  participant T as Agent 工具表
  U->>UI: 选择插件并安装
  UI->>H: Preview + Install
  H-->>UI: 已安装，可能需要设置
  UI->>H: 保存配置/完成授权
  H->>R: Enable + Preflight
  R->>M: ApplySource(plugin:id)
  M->>M: 启动、握手、tools/list
  M->>T: 注册工具
  M-->>R: Connected + tool names
  R-->>UI: Ready / Degraded / Failed
  U->>UI: 点击“试用插件”
  UI->>T: 发起受权限策略约束的测试
  T-->>UI: 结果/报错/诊断
~~~

异常规则：

- 缺依赖、未授权、配置缺失：标记 Needs Setup，不循环连接。
- 握手超时/子进程退出：标记 Failed，提供 stderr 尾部和“重试连接”；可有限次数自动退避，避免无限重启。
- 工具注册部分成功：标记 Degraded，显示实际工具数和冲突工具名。
- 关闭插件：取消未完成登录或动作，断开 MCP，移除注册工具；确保不可在 disabled 状态继续执行插件 action。
- 插件升级：原子替换 + 失败回滚旧版本，复查权限与依赖，避免“升级后灰屏”。

## 10. 分阶段落地（按 PR 拆分）

| 阶段 | 代码改动 | 可验收结果 |
| --- | --- | --- |
| **P0：修真实问题** | disabled action/login 防护；聚合 MCP 状态到插件详情；内置市场离线兜底；远程市场使用应用代理；依赖 probe | 安装 echo-mcp 且没 Python 时明确显示 Needs Setup；安装后 Python 可连接，能看到工具；禁用后不可运行 |
| **P1：可用性闭环** | 安装后向导；状态汇总；显示能力数量；“如何使用 / 一键试用”；重连、错误码、脱敏日志 | 新用户不用查文档就能用 hello-skill 和 echo-mcp |
| **P2：稳定和兼容** | 配置变化重连、进程退出重试、工具级健康、跨模型工具兼容诊断、远程 MCP OAuth、源版本锁定 | 飞书、HTTP MCP 的失败能定位到依赖/登录/握手/模型，不混淆 |
| **P3：生态扩展** | v2 manifest、Codex/Claude 插件导入适配、来源校验、版本更新与回滚、可选更严格运行隔离 | 插件来源可追踪，升级可回滚，旧 v1 插件兼容 |
| **P4：高级拓展（可选）** | hooks/subagents 的安全策略与资源预算；受控的声明式 UI | 没有绕开宿主权限的隐式执行路径 |

优先处理 P0/P1，不要先做一个漂亮但缺少运行时状态的市场页面。

## 11. 测试与验收标准

至少建立以下端到端场景，除了 Go 单测和 React Vitest，还需要桌面端 Wails 真正执行的验证：

1. **纯 Skill**：安装 hello-skill → Ready → 输入 /hello → 生成预期模板 → 停用后菜单和技能消失。
2. **缺依赖**：Windows 干净环境未安装 Python → 安装 echo-mcp → Needs Setup（而非就绪）→ 给出修复建议 → 安装并检测 Python → 工具注册。
3. **MCP**：stdio 握手、tools/list、echo 工具调用真实经过权限审批；连接退出/超时后显示错误，再重试可恢复。
4. **飞书**：缺 lark-cli / 未登录 / 登录中 / 已登录但服务失败分别呈现，敏感读取始终需确认；真实租户接入必须独立验收。
5. **模型兼容**：模拟 Provider 返回不支持 tools 的 HTTP 400，清楚区分模型问题与 MCP 连接问题，不泄露工具输入。
6. **生命周期**：配置更新 → 只重启该插件 MCP；停用 → 进程与工具全部退出，不能再执行 plugin action/login。
7. **市场与代理**：网络直连失败而应用代理可用、网络离线回退内置 catalog、Git 地址不可信、非法清单和 zip slip。
8. **安全与数据**：secret 不进入前端状态/日志；恶意链接不被执行；工具输出的 prompt injection 不提升权限。
9. **回归**：Windows/macOS/Linux CI，以及 Windows 实机安装新版本后的首次运行路径。
10. **可诊断**：从 UI 一键导出脱敏诊断，工程师无需用户重复截图就能看到插件 ID、依赖检查、MCP 连接状态、工具数量、最新失败原因。

验收定义：**Installed 不等于 Ready。每个已启用插件必须能明确回答“哪些能力已就绪、如何试用、不可用原因是什么”，并且停用后所有可执行入口都失效。**

## 12. 与现有仓库的对照与直接落点

- internal/plugin/host.go：增强 Info + Health 汇总，guard state，发布配置/启停变化事件。
- internal/mcp/manager.go：复用 Statuses/Source/Tools，增加按来源的健康查询、可控重连、异常事件与诊断。
- internal/plugin/catalog.go：引入应用内 HTTP 代理，输出来源/版本/更新提示。
- internal/bootstrap/bootstrap.go：将插件 Host、MCP Manager、设置代理和健康汇总装配。
- internal/app/plugins.go：增加 Wails 诊断、试用、重连、获取能力绑定。
- frontend/src/components/PluginSettings.tsx：安装向导、详情页健康状态、工具列表、“试用/重连/复制诊断”。
- frontend/src/components/Composer.tsx：已启用能力的可发现入口和示例消息插入。
- catalog/bundled/*：明确 Skill-only 与 MCP 插件差异，为内置插件补齐 smoke test 和使用说明。
- docs/plugin-development.md：更新 manifest v2 增量字段、测试指南、兼容清单。

## 13. 外部设计参考（不要求直接复制其运行时）

- OpenAI Plugins 架构（Skills + MCP + 可选 UI）：https://developers.openai.com/plugins/concepts/plugins
- Skills 与 MCP 的职责区分：https://developers.openai.com/plugins/concepts/skills
- 可移植插件包与 MCP 声明：https://developers.openai.com/plugins/build/plugins
- Claude Code 命令与插件管理入口：https://code.claude.com/docs/zh-CN/commands

Mosaic 的差异化应是 **桌面本地优先、跨模型、可视化工具执行和可恢复的插件运行状态**，而不是重新创建一套与 MCP 不兼容的专有工具协议。
