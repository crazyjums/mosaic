import { describe, expect, it, vi } from "vitest";
import { createDemoPlugins } from "./demoPlugins";

// 演示后端的插件行为要和真实后端（internal/plugin）一致，界面才能在浏览器里预览和截图。
describe("演示模式的插件", () => {
  it("预览不生效；确认后安装并启用；风险只有勾选了才降级", async () => {
    const b = createDemoPlugins();
    const p = await b.previewPlugin("/x/feishu");
    expect(await b.listPlugins()).toHaveLength(0);
    expect(p.plugin.servers[0]).toMatchObject({ declared_risk: "read", effective_risk: "exec", approved: false });

    const info = await b.installPlugin(p.token, []);
    expect(info.enabled).toBe(true);
    expect(info.servers[0].effective_risk).toBe("exec");
    await expect(b.installPlugin(p.token, [])).rejects.toThrow("失效");

    const p2 = await b.previewPlugin("/x/feishu");
    expect(p2.existing?.version).toBe("0.1.0"); // 覆盖更新
    const again = await b.installPlugin(p2.token, ["feishu"]);
    expect(again.servers[0]).toMatchObject({ approved: true, effective_risk: "read" });
  });

  it("校验失败直接报错，取消预览后不能再安装", async () => {
    const b = createDemoPlugins();
    await expect(b.previewPlugin("/x/bad-plugin")).rejects.toThrow("JSON");
    const p = await b.previewPlugin("/x/hello");
    await b.cancelPluginPreview(p.token);
    await expect(b.installPlugin(p.token, [])).rejects.toThrow();
  });

  it("配置：secret 只记“已设置”，空串不修改，非法值被拒绝；缺必填项有提示", async () => {
    const b = createDemoPlugins();
    await b.installPlugin((await b.previewPlugin("/x/feishu")).token, []);
    expect((await b.listPlugins())[0].warning).toContain("App Secret");

    const saved = await b.savePluginConfig("feishu", { app_secret: "s", region: "global" });
    expect(saved.warning).toBeUndefined();
    const secret = saved.config.find((c) => c.key === "app_secret")!;
    expect(secret).toMatchObject({ has_secret: true, value: "" });
    expect(saved.config.find((c) => c.key === "region")!.value).toBe("global");

    expect((await b.savePluginConfig("feishu", { app_secret: "" })).config.find((c) => c.key === "app_secret")!.has_secret).toBe(true);
    await expect(b.savePluginConfig("feishu", { region: "mars" })).rejects.toThrow("可选项");
    await expect(b.savePluginConfig("feishu", { readonly: "yes" })).rejects.toThrow("true");
    expect((await b.clearPluginSecret("feishu", "app_secret")).config.find((c) => c.key === "app_secret")!.has_secret).toBe(false);
  });

  it("状态和操作：缺依赖时不运行", async () => {
    const b = createDemoPlugins();
    await b.installPlugin((await b.previewPlugin("/x/feishu")).token, []);
    expect(await b.getPluginStatus("feishu")).toMatchObject({ state: "missing_dependency", missing: ["lark-cli"] });
    expect((await b.runPluginAction("feishu", "init")).error).toContain("lark-cli");
    await expect(b.runPluginAction("feishu", "nope")).rejects.toThrow();
  });

  it("命令跟着启用状态出现和消失，展开成提示词", async () => {
    const b = createDemoPlugins();
    await b.installPlugin((await b.previewPlugin("/x/hello")).token, []);
    expect((await b.listCommands()).map((c) => c.name)).toEqual(["hello"]);
    expect(await b.expandCommand("/Hello", " 世界 ")).toEqual({ found: true, text: "（示例命令 hello）世界" });
    expect((await b.expandCommand("review", "x")).found).toBe(false);
    await b.setPluginEnabled("hello", false);
    expect(await b.listCommands()).toEqual([]);
    expect((await b.expandCommand("hello", "")).found).toBe(false);
    await b.uninstallPlugin("hello");
    expect(await b.listPlugins()).toEqual([]);
  });

  it("V2 诊断将已安装/已启用与真实可用区分开，停用后拒绝运行操作", async () => {
    const b = createDemoPlugins();
    await b.installPlugin((await b.previewPlugin("/x/hello")).token, []);
    const ready = await b.getPluginDiagnostics("hello");
    expect(ready).toMatchObject({ state: "ready", enabled: true, tool_count: 0, commands: ["hello"] });
    expect((await b.listPluginDiagnostics())[0].state).toBe("ready");
    await b.installPlugin((await b.previewPlugin("/x/feishu")).token, []);
    expect(await b.getPluginDiagnostics("feishu")).toMatchObject({ state: "needs_setup", code: "DEPENDENCY_NOT_FOUND" });
    await b.setPluginEnabled("feishu", false);
    expect(await b.getPluginDiagnostics("feishu")).toMatchObject({ state: "disabled", code: "PLUGIN_DISABLED" });
    await expect(b.runPluginAction("feishu", "init")).rejects.toThrow("已停用");
    await expect(b.startPluginLogin("feishu")).rejects.toThrow("已停用");
    await expect(b.reconnectPlugin("feishu")).rejects.toThrow();
  });

  it("插件市场：列出内置和 Git 来源，装好后标记已安装；Git 地址只收 https", async () => {
    const b = createDemoPlugins();
    const list = await b.listPluginCatalog();
    expect(list.map((c) => [c.id, c.kind, c.installed]).slice(0, 2)).toEqual([["feishu", "git", false], ["code-review", "bundled", false]]);
    expect(list.find((c) => c.featured)?.id).toBe("feishu");
    expect(list.every((c) => !!c.category)).toBe(true);

    const p = await b.previewCatalogPlugin("hello");
    expect(await b.listPlugins()).toHaveLength(0); // 预览不生效
    await b.installPlugin(p.token, []);
    const after = await b.refreshPluginCatalog();
    expect(after.find((c) => c.id === "hello")).toMatchObject({ installed: true, installed_version: "0.1.0" });
    await expect(b.previewCatalogPlugin("nope")).rejects.toThrow("没有插件");

    await expect(b.previewPlugin("http://github.com/a/b")).rejects.toThrow("https://");
    expect((await b.previewPlugin("https://github.com/a/feishu#v1")).plugin.id).toBe("feishu");
  });

  it("新建插件模板：ID 不合法时报错，合法时返回路径", async () => {
    const b = createDemoPlugins();
    await expect(b.scaffoldPlugin("/tmp", "Bad_ID")).rejects.toThrow("插件 ID");
    expect(await b.scaffoldPlugin("/tmp/", "my-plugin")).toBe("/tmp/my-plugin");
  });

  it("一键登录：点击后等待授权，完成后状态变为已登录；可取消、可重新开始", async () => {
    vi.useFakeTimers();
    try {
      const b = createDemoPlugins();
      await b.installPlugin((await b.previewPlugin("/x/feishu")).token, []);
      expect((await b.listPlugins())[0].login).toMatchObject({ label: "登录飞书" });
      expect(await b.getPluginLoginState("feishu")).toEqual({ state: "idle" });

      const st = await b.startPluginLogin("feishu");
      expect(st).toMatchObject({ state: "running", url: expect.stringContaining("https://") });
      expect((await b.startPluginLogin("feishu")).url).toBe(st.url); // 等待中再点：同一次登录
      await vi.advanceTimersByTimeAsync(3000);
      expect(await b.getPluginLoginState("feishu")).toMatchObject({ state: "ok" });
      expect((await b.getPluginStatus("feishu")).message).toContain("已登录");

      await b.startPluginLogin("feishu");
      await b.cancelPluginLogin("feishu");
      await vi.advanceTimersByTimeAsync(3000);
      expect((await b.getPluginLoginState("feishu")).state).toBe("cancelled"); // 取消后不会被定时器改回去

      await b.installPlugin((await b.previewPlugin("/x/hello")).token, []);
      await expect(b.startPluginLogin("hello")).rejects.toThrow("没有登录流程");
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("演示：插件合集（marketplace）", () => {
  it("识别为合集，列出可装和不可装（带原因）的条目；挑一个后进入普通的安装确认", async () => {
    const b = createDemoPlugins();
    const r = await b.inspectPluginSource("https://github.com/example/market-tools");
    expect(r.kind).toBe("marketplace");
    const m = r.market!;
    expect(m.plugins.filter((p) => p.supported).map((p) => p.name)).toEqual(["hello", "feishu"]);
    const bad = m.plugins.filter((p) => !p.supported);
    expect(bad.length).toBe(2);
    expect(bad.every((p) => !!p.reason)).toBe(true);
    await expect(b.previewMarketplacePlugin(m.token, "formatter")).rejects.toThrow("暂时不能安装");
    const p = await b.previewMarketplacePlugin(m.token, "hello");
    expect(p.plugin.id).toBe("hello");
    const info = await b.installPlugin(p.token, []);
    expect(info.enabled).toBe(true);
  });

  it("取消之后选择失效；不是合集的来源照常当作一个插件", async () => {
    const b = createDemoPlugins();
    const m = (await b.inspectPluginSource("/path/to/market")).market!;
    await b.cancelPluginMarketplace(m.token);
    await expect(b.previewMarketplacePlugin(m.token, "hello")).rejects.toThrow("失效");
    const one = await b.inspectPluginSource("/path/to/hello-plugin");
    expect(one.kind).toBe("plugin");
    expect(one.preview?.plugin.id).toBe("hello");
  });
});

describe("演示：插件的试用", () => {
  it("涉及私密内容的工具不能试调用：写明原因，直接调用也被拒绝", async () => {
    const b = createDemoPlugins();
    const p = await b.previewPlugin("/x/feishu");
    await b.installPlugin(p.token, []);
    const tools = await b.listPluginTools("feishu");
    expect(tools).toHaveLength(1);
    expect(tools[0].trial).toBe(false);
    expect(tools[0].reason).toContain("私密");
    await expect(b.tryPluginTool("feishu", tools[0].name)).rejects.toThrow("不能试调用");
    await expect(b.tryPluginTool("feishu", "mcp__feishu__nope")).rejects.toThrow("没有注册工具");
  });

  it("没安装或已停用的插件一律拒绝；没有 MCP 服务的插件没有工具可试", async () => {
    const b = createDemoPlugins();
    await expect(b.listPluginTools("feishu")).rejects.toThrow("没有安装");
    const p = await b.previewPlugin("/x/feishu");
    await b.installPlugin(p.token, []);
    await b.setPluginEnabled("feishu", false);
    await expect(b.listPluginTools("feishu")).rejects.toThrow("未启用");
    const h = await b.previewPlugin("/x/hello");
    await b.installPlugin(h.token, []);
    await expect(b.listPluginTools("hello")).rejects.toThrow("未启用或无法加载");
  });
});
