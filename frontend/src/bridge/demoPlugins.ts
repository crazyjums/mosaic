import type { PluginActionResult, PluginInfo, PluginPreview, PluginStatus, SlashCommandInfo } from "./types";

// 演示模式（浏览器预览 / 截图 / 测试）里的插件：内存里的假实现，行为尽量和后端 internal/plugin 一致。
// 选择路径里带 "hello" 得到一个只有技能和命令的插件，带 "bad" 得到校验失败，其余得到一个带 MCP 工具和配置表单的“飞书”示例。

const hello = (): PluginInfo => ({
  id: "hello", name: "问候示例", version: "0.1.0", description: "一个不含可执行代码的插件：一个技能和一个斜杠命令。",
  enabled: false, skills: ["greeting"], commands: [{ name: "hello", description: "用一句话问候" }], servers: [], config: [],
  has_status: false, actions: [], requires: [], network: [],
});

const feishu = (): PluginInfo => ({
  id: "feishu", name: "飞书", version: "0.1.0", description: "读取飞书文档和群消息（示例）。",
  enabled: false, skills: ["feishu-docs"], commands: [{ name: "feishu-summary", description: "总结一篇飞书文档" }],
  servers: [{ name: "feishu", transport: "stdio", command: "node ${PLUGIN_DIR}/server/feishu-mcp.js --profile ${config.profile}", declared_risk: "read", effective_risk: "exec", approved: false }],
  config: [
    { key: "profile", label: "飞书应用配置名", type: "string", default: "default", value: "default", has_secret: false, help: "lark-cli 里配置的 profile 名" },
    { key: "region", label: "区域", type: "select", options: ["cn", "global"], default: "cn", value: "cn", has_secret: false },
    { key: "app_secret", label: "App Secret", type: "secret", required: true, value: "", has_secret: false, help: "只保存在系统钥匙串里，界面不会再显示" },
    { key: "readonly", label: "只读模式", type: "bool", default: "true", value: "true", has_secret: false },
  ],
  has_status: true, actions: [{ id: "login", label: "登录飞书" }],
  requires: [{ command: "lark-cli", found: false }, { command: "node", found: true }], network: ["open.feishu.cn"],
});

export function createDemoPlugins() {
  let installed: PluginInfo[] = [];
  const staged = new Map<string, PluginPreview>();
  let n = 0;
  const find = (id: string) => {
    const p = installed.find((x) => x.id === id);
    if (!p) throw new Error(`没有安装插件 "${id}"`);
    return p;
  };
  const replace = (p: PluginInfo) => {
    installed = installed.map((x) => (x.id === p.id ? p : x));
    return p;
  };
  const warn = (p: PluginInfo): PluginInfo => {
    const missing = p.config.find((c) => c.required && c.type === "secret" && !c.has_secret);
    return { ...p, warning: p.enabled && missing ? `还没有填写必填项「${missing.label}」` : undefined };
  };

  return {
    listPlugins: async (): Promise<PluginInfo[]> => installed.map(warn),
    previewPlugin: async (src: string): Promise<PluginPreview> => {
      if (src.includes("bad")) throw new Error("plugin.json 不是合法的 JSON：invalid character '}' looking for beginning of object key string");
      const plugin = src.includes("hello") ? hello() : feishu();
      const existing = installed.find((x) => x.id === plugin.id);
      const token = `tok${++n}`;
      const p: PluginPreview = { token, plugin, existing: existing && warn(existing) };
      staged.set(token, p);
      return p;
    },
    installPlugin: async (token: string, approved: string[]): Promise<PluginInfo> => {
      const p = staged.get(token);
      if (!p) throw new Error("这次安装已经失效，请重新选择插件");
      staged.delete(token);
      const old = installed.find((x) => x.id === p.plugin.id);
      const next: PluginInfo = {
        ...p.plugin,
        enabled: true,
        servers: p.plugin.servers.map((s) => ({ ...s, approved: approved.includes(s.name), effective_risk: approved.includes(s.name) ? s.declared_risk : "exec" })),
        // 覆盖更新保留配置
        config: p.plugin.config.map((c) => old?.config.find((o) => o.key === c.key) ?? c),
        installed_at: new Date().toISOString(),
      };
      installed = old ? installed.map((x) => (x.id === next.id ? next : x)) : [...installed, next];
      return warn(next);
    },
    cancelPluginPreview: async (token: string): Promise<void> => {
      staged.delete(token);
    },
    setPluginEnabled: async (id: string, on: boolean): Promise<PluginInfo> => warn(replace({ ...find(id), enabled: on })),
    uninstallPlugin: async (id: string): Promise<void> => {
      find(id);
      installed = installed.filter((x) => x.id !== id);
    },
    savePluginConfig: async (id: string, values: Record<string, string>): Promise<PluginInfo> => {
      const p = find(id);
      const config = p.config.map((c) => {
        const v = values[c.key];
        if (v === undefined) return c;
        if (c.type === "secret") return v === "" ? c : { ...c, has_secret: true };
        if (c.type === "bool" && v !== "true" && v !== "false") throw new Error(`${c.label} 的值必须是 true 或 false`);
        if (c.type === "select" && !c.options?.includes(v)) throw new Error(`${c.label} 的值 "${v}" 不在可选项里`);
        return { ...c, value: v };
      });
      return warn(replace({ ...p, config }));
    },
    clearPluginSecret: async (id: string, key: string): Promise<PluginInfo> => {
      const p = find(id);
      return warn(replace({ ...p, config: p.config.map((c) => (c.key === key ? { ...c, has_secret: false } : c)) }));
    },
    getPluginStatus: async (id: string): Promise<PluginStatus> => {
      const p = find(id);
      if (!p.has_status) return { state: "none" };
      const missing = p.requires.filter((r) => !r.found).map((r) => r.command);
      if (missing.length) return { state: "missing_dependency", missing, message: `缺少依赖：${missing.join("、")}` };
      return p.config.some((c) => c.type === "secret" && !c.has_secret) ? { state: "bad", message: '{"ok":false}' } : { state: "ok", message: '{"ok":true,"user":"demo"}' };
    },
    runPluginAction: async (id: string, action: string): Promise<PluginActionResult> => {
      const p = find(id);
      if (!p.actions.some((a) => a.id === action)) throw new Error(`插件没有操作 "${action}"`);
      const miss = p.requires.filter((r) => !r.found).map((r) => r.command);
      if (miss.length) return { output: "", exit_code: 0, error: `缺少依赖：${miss.join("、")}` };
      return { output: "请在浏览器里打开下面的链接完成授权：\nhttps://open.feishu.cn/oauth/authorize?demo=1\n等待授权完成…", exit_code: 0 };
    },
    listCommands: async (): Promise<SlashCommandInfo[]> =>
      installed.filter((p) => p.enabled && !p.error).flatMap((p) => p.commands.map((c) => ({ name: c.name, description: c.description ?? "", plugin_id: p.id, plugin_name: p.name }))),
    expandCommand: async (name: string, args: string): Promise<{ found: boolean; text: string }> => {
      const n = name.replace(/^\//, "").toLowerCase();
      const cmd = installed.filter((p) => p.enabled && !p.error).flatMap((p) => p.commands).find((c) => c.name.toLowerCase() === n);
      return cmd ? { found: true, text: `（示例命令 ${cmd.name}）${args.trim()}` } : { found: false, text: "" };
    },
  };
}
