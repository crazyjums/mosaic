# 插件开发指南

这份指南教你给 Mosaic 写插件。插件是**一个文件夹加一个 `plugin.json`**，装上之后可以给 agent 增加：

| 想要 | 用什么 | 要写代码吗 |
| --- | --- | --- |
| 教模型一套做法（评审清单、写作规范…） | 技能（`SKILL.md`） | 不用 |
| 输入 `/名字` 触发一段固定的提示词 | 斜杠命令（一个 `.md` 模板） | 不用 |
| 让模型能调用你的程序（查系统、调命令行…） | MCP 工具（一个 MCP 服务，任意语言） | 要 |
| 让用户填密钥或选项 | 配置表单（`config`） | 不用 |
| 显示“是否正常”、加几个按钮 | 状态检查和操作（`status` / `actions`） | 一条命令 |

设计细节和安全模型见 [design/plugin-system.md](design/plugin-system.md)；这里只讲怎么写。

## 5 分钟上手

1. 打开 **设置 → 插件**，点“**新建插件模板…**”，填一个 ID（小写字母、数字、`-`），选一个放它的目录。
   会生成一个带技能和命令的最小插件。
2. 改里面的文字（`plugin.json` 的 name / description，`skills/<id>/SKILL.md`，`commands/<id>.md`）。
3. 点“**安装文件夹…**”，选这个目录，在确认框里点安装。改完文件后再装一次就是更新。
4. 在输入框键入 `/`，能看到你的命令；技能会出现在模型的技能清单里，模型觉得合适时自己会读。

仓库里的 `catalog/bundled/` 有可以直接参考的例子：

- `hello-skill`：技能加命令，最简单。
- `code-review`、`git-assistant`、`writing-kit`、`debug-helper`、`sql-helper`：官方的纯技能 / 命令插件，看它们怎么写 `SKILL.md`。
- `echo-mcp`：一个 Python 写的最小 MCP 服务，带配置表单、状态检查和按钮。
- `feishu`：包装命令行工具（lark-cli）的 MCP 服务，演示私密工具、登录流程（按钮分步）、只读白名单。

## 目录结构

```
my-plugin/
  plugin.json                  清单（必须在最外层）
  skills/<名字>/SKILL.md       技能（可多个）
  commands/<名字>.md           斜杠命令（可多个）
  server/…                     你的 MCP 服务（可选，放哪都行，路径用 ${PLUGIN_DIR} 引用）
```

打成 `.zip` 也可以：清单可以在压缩包最外层，也可以在唯一的一个顶层目录里（GitHub 下载的 zip 就是这样）。

## plugin.json

```json
{
  "id": "my-plugin",
  "name": "我的插件",
  "version": "0.1.0",
  "description": "一句话说明",
  "min_app_version": "1.3.2",
  "protocol": 1,
  "skills":   [{ "path": "skills/my-skill" }],
  "commands": [{ "name": "my-cmd", "path": "commands/my-cmd.md", "description": "一句话说明" }],
  "mcp":      [{ "command": "python3", "args": ["${PLUGIN_DIR}/server/main.py"], "risk": "read" }],
  "config":   [{ "key": "token", "label": "访问令牌", "type": "secret", "required": true }],
  "status":   { "command": "python3 ${PLUGIN_DIR}/server/main.py --status", "ok_path": "ok" },
  "actions":  [{ "id": "login", "label": "登录", "command": "python3 ${PLUGIN_DIR}/server/main.py --login" }],
  "requires": { "commands": ["python3"] },
  "network":  ["api.example.com"]
}
```

- **id**：小写字母、数字、`-`，以字母或数字开头，最长 24 个字符。它会成为 MCP 服务名和工具名的一部分，装好后不要改。
- **version**：安装确认框和插件详情里显示；覆盖安装时用来区分新旧。
- **min_app_version**：低于这个版本的应用会显示“需要升级应用”并不加载。
- **protocol**：目前固定写 `1`。
- 清单最大 256KB；技能和命令各最多 50 个，`mcp` 最多 8 个，`config` 最多 30 项。
- **暂不支持的字段**：`hooks`、`subagents`。写了会被明确拒绝（而不是悄悄忽略）。

### 变量

在 `mcp`（`command`、`args`、`env`、`cwd`）和 `status` / `actions` 的命令里可以用：

- `${PLUGIN_DIR}`：插件目录（装好后只读）；
- `${DATA_DIR}`：插件自己的数据目录（可写，卸载时可选择一并清除）；
- `${config.xxx}`：配置表单里 `xxx` 项的值。

写了不认识的变量是校验错误，不会被原样留下。命令**不经过 shell**：按空格拆成参数（支持双引号），所以不能用管道、`&&`、重定向；需要的话自己写个脚本再调用。

## 技能（SKILL.md）

```markdown
---
name: code-review
description: 用户要求评审、检查代码改动时使用。
---

具体做法……
```

- `description` 最重要：系统提示词里只放“名称 + 这句话”，模型靠它判断要不要读正文。写清楚**什么时候该用**。
- 正文写具体步骤和输出格式，越具体越好；需要运行命令时直接让模型用 shell 工具，权限照常确认。
- 同名技能的优先级：项目 > 用户 > 插件，插件的技能不会覆盖用户自己写的。

## 斜杠命令（commands/*.md）

```markdown
---
description: 菜单里显示的一句话说明
---

发给模型的提示词，用户在命令后面输入的内容会替换这里：{{args}}
```

- 文件头的 `---` 块可以省略。`{{args}}` 可以不写：不写时，用户输入的内容会接在模板后面，不会丢掉。
- 命令名用小写字母、数字、`.`、`_`、`-`。不同插件的命令重名时，菜单里会加插件前缀（`/插件id:命令名`）。

## MCP 工具

`mcp` 里每一项声明一个由应用启动、托管的 MCP 服务（stdio）。你的程序只要按 [MCP 协议](https://modelcontextprotocol.io) 通过标准输入输出说话就行，语言不限。

- 工具名是 `mcp__<服务名>__<工具名>`，服务名是插件 id（有多个服务时是 `<插件id>-<name>`）。
- **风险等级**：默认每次调用都询问。`"risk": "read"` 只是“申请”：用户在**安装确认框里逐项勾选**后才生效，没勾选仍然每次询问。只在工具确实只读时才申请。
- **环境变量**：子进程只拿到 PATH、HOME、语言和临时目录这类基础变量，加上你在 `env` 里声明的；**拿不到应用自己的环境**。需要什么就在 `env` 里声明，通常来自 `${config.xxx}`。
- **私密工具**：服务的工具会读取私密内容（聊天记录、文档正文…）、读到的会发给模型厂商时，写 `"sensitive": true`。这样每次调用都会询问用户，不能设为“本会话总是允许”，也不受权限档位和规则放行影响。它只会让流程更严格，所以不需要用户在安装时另行确认；需要 `min_app_version` 不低于支持它的版本（1.5.0）。
- 参考 `catalog/bundled/echo-mcp/server/echo_server.py`（只用标准库，100 行左右）。

## 配置表单（config）

| type | 说明 |
| --- | --- |
| `string` | 单行文字 |
| `secret` | 密钥：只存系统钥匙串，界面保存后只显示“已设置”，永远不会再把值发回界面；也没有文件备份 |
| `bool` | 开关，值是 `"true"` / `"false"` |
| `select` | 下拉，需要 `options` |
| `path` | 文件或目录路径 |

可用字段：`key`（小写字母开头）、`label`、`default`、`required`、`help`。`required` 的 secret 没填时，插件详情里会提示。

## 状态检查和操作

- `status.command` 输出一段 JSON，`ok_path` 用点号路径指向其中的布尔字段（如 `"ok"` 或 `"auth.ok"`）。界面显示“正常 / 异常 / 缺少依赖”。打开详情、保存配置后以及每 30 秒会检查一次，超时 15 秒。
- `actions` 是详情页的按钮，点击运行命令（超时 120 秒），输出显示在面板里，输出里的 http(s) 链接可以点（适合放登录授权地址）。
- 两者都在插件目录里运行，同样只拿到白名单环境变量和你声明的 `env`，输出最多 100KB。
- `requires.commands` 里的程序在本机找不到时，详情页会提示，并且状态检查和按钮不会执行。

## 安装确认和安全

用户装插件之前会看到：它要启动的命令、提供的工具和你声明的风险（每个 `read` 单独勾选）、依赖的程序是否存在、你声明的联网域名、需要填的配置项、带来的技能和命令。**插件是在用户电脑上运行的外部程序，应用无法限制它能做什么**，所以：

- 只申请你真正需要的东西；`network` 里如实写你会访问的域名。
- 不要把密钥写进代码或日志；用 `secret` 配置，由 `${config.xxx}` 通过 `env` 传给你的程序。
- 路径必须在插件目录内（符号链接会被拒绝）；zip 里不能有符号链接或指向目录外的路径。
- 读到的内容（文档、消息…）会作为工具结果发给用户当前使用的模型厂商，涉及敏感内容的工具要在说明里讲清楚。

## 分发

1. **给自己或同事**：把目录打成 zip，或放进 Git 仓库。别人用“安装 zip…”或“从 Git 地址安装…”（`https://…#分支或标签`，建议用标签，别人装到的就是固定版本）。
2. **进插件市场**：给仓库提 PR，在 `catalog/registry.json` 里加一项：

   ```json
   { "id": "my-plugin", "name": "我的插件",
     "summary": { "zh": "一句话说明", "en": "One line" },
     "git": "https://github.com/你/仓库", "ref": "v0.1.0", "subdir": "plugins/my-plugin" }
   ```

   `ref`、`subdir` 可省略。官方内置的插件则是放进 `catalog/bundled/<id>/`，并写 `"bundled": "<id>"`。
   列表项合并后，应用打开插件页时会在后台刷新到。用户装之前仍会看到完整的确认框。

## 常见问题

- **装不上，提示清单不合法**：错误信息会指出哪个字段。常见的是路径写成了绝对路径或带 `..`、用了未声明的 `${config.xxx}`、id 里有大写或下划线。
- **命令没出现在 `/` 菜单**：插件被停用了，或命令名和别的插件重名（看带前缀的那个）。
- **MCP 工具没出现**：设置 → MCP 里能看到插件带来的服务和它的连接状态、失败原因；先在终端里直接运行你的启动命令看看能不能起来。
- **程序找不到**：插件的子进程只有基础的 PATH，用到的程序放进 `requires.commands`，让界面提前提示。
- **想改一个已装插件**：改文件后再“安装文件夹…”一次，配置会保留。
