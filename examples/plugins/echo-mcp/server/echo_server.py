#!/usr/bin/env python3
"""一个最小的 MCP 服务器（stdio，按行传 JSON-RPC），只依赖 Python 标准库。

演示 Mosaic 插件如何提供外部工具：插件清单里声明启动命令，应用负责启动、连接、把工具接进工具表，
并让每次调用走统一的权限确认。

    python3 echo_server.py --prefix "echo:"   作为 MCP 服务器运行（应用会这样启动它）
    python3 echo_server.py --status          打印 JSON 状态（给清单里的 status 用）
    python3 echo_server.py --selftest        自检，打印结果（给清单里的 actions 用）
"""
import json
import os
import sys

PROTOCOL = "2024-11-05"


def tools(prefix):
    loud = os.environ.get("ECHO_LOUD") == "true"
    return {
        "echo": ("原样返回文字，前面加上设置里的前缀。" + ("（大声模式：转成大写）" if loud else ""),
                 lambda text: prefix + " " + (text.upper() if loud else text)),
        "shout": ("把文字转成大写返回。", lambda text: text.upper() + "!"),
    }


def handle(msg, prefix):
    method, mid = msg.get("method"), msg.get("id")
    if method == "initialize":
        return {"protocolVersion": PROTOCOL, "capabilities": {"tools": {}}, "serverInfo": {"name": "echo-mcp", "version": "0.1.0"}}
    if method == "tools/list":
        return {"tools": [
            {"name": n, "description": d, "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}}
            for n, (d, _) in tools(prefix).items()
        ]}
    if method == "tools/call":
        params = msg.get("params", {})
        tool = tools(prefix).get(params.get("name"))
        if tool is None:
            return {"content": [{"type": "text", "text": "unknown tool"}], "isError": True}
        text = str(params.get("arguments", {}).get("text", ""))
        return {"content": [{"type": "text", "text": tool[1](text)}]}
    if method == "ping":
        return {}
    return None if mid is None else {"__error": {"code": -32601, "message": "method not found: %s" % method}}


def serve(prefix):
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)
        if "id" not in msg:  # 通知（如 notifications/initialized）不需要回复
            continue
        result = handle(msg, prefix)
        reply = {"jsonrpc": "2.0", "id": msg["id"]}
        if isinstance(result, dict) and "__error" in result:
            reply["error"] = result["__error"]
        else:
            reply["result"] = result
        sys.stdout.write(json.dumps(reply, ensure_ascii=False) + "\n")
        sys.stdout.flush()


def main():
    args = sys.argv[1:]
    prefix = args[args.index("--prefix") + 1] if "--prefix" in args else "echo:"
    if "--status" in args:
        print(json.dumps({"ok": True, "python": sys.version.split()[0]}))
    elif "--selftest" in args:
        out = tools(prefix)["echo"][1]("hello")
        print("echo(\"hello\") ->", out)
        print("自检通过" if out.endswith("hello") or out.endswith("HELLO") else "自检失败")
    else:
        serve(prefix)


if __name__ == "__main__":
    main()
