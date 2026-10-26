import { describe, expect, it } from "vitest";
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
    expect((await b.runPluginAction("feishu", "login")).error).toContain("lark-cli");
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

  it("插件市场：列出内置和 Git 来源，装好后标记已安装；Git 地址只收 https", async () => {
    const b = createDemoPlugins();
    const list = await b.listPluginCatalog();
    expect(list.map((c) => [c.id, c.kind, c.installed])).toEqual([["hello", "bundled", false], ["feishu", "git", false]]);

    const p = await b.previewCatalogPlugin("hello");
    expect(await b.listPlugins()).toHaveLength(0); // 预览不生效
    await b.installPlugin(p.token, []);
    const after = await b.refreshPluginCatalog();
    expect(after[0]).toMatchObject({ installed: true, installed_version: "0.1.0" });
    await expect(b.previewCatalogPlugin("nope")).rejects.toThrow("没有插件");

    await expect(b.previewPlugin("http://github.com/a/b")).rejects.toThrow("https://");
    expect((await b.previewPlugin("https://github.com/a/feishu#v1")).plugin.id).toBe("feishu");
  });

  it("新建插件模板：ID 不合法时报错，合法时返回路径", async () => {
    const b = createDemoPlugins();
    await expect(b.scaffoldPlugin("/tmp", "Bad_ID")).rejects.toThrow("插件 ID");
    expect(await b.scaffoldPlugin("/tmp/", "my-plugin")).toBe("/tmp/my-plugin");
  });
});
