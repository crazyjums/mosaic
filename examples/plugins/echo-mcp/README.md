# echo-mcp

一个最小的 MCP 工具插件，演示第 2 期的全部能力：

- `mcp`：启动 `server/echo_server.py`，它提供 `echo` / `shout` 两个工具（工具名是 `mcp__echo-mcp__echo` 等）；
  清单里声明了 `risk: read`，安装确认时勾选后才按只读运行，不勾选则每次调用都会询问；
- `config`：设置表单（字符串、开关），值通过 `${config.xxx}` 传给服务器；
- `status`：用 `--status` 检查服务器能不能运行；
- `actions`：“运行自检”按钮；
- `requires`：声明依赖 `python3`，缺失时在插件页提示。

安装：设置 → 插件 → 安装文件夹…，选择这个目录。
