import { describe, expect, it, vi } from "vitest";

// 渲染组件会（经 FileLink）引入 chatStore，store 创建时会选择后端；测试环境没有 window，用一个空后端顶替。
vi.mock("../bridge", () => ({ backend: () => ({ kind: "demo", onEvent: () => () => {} }) }));
const { IMAGE_EXT, isLocalPath, normalizeLocalPath, svgDataURL } = await import("./MediaView");
const { isTitleOnly } = await import("./ThinkingCard");

describe("聊天里的图片", () => {
  it("区分本地路径和网址", () => {
    for (const p of ["效果图.svg", "./a/b.png", "docs/x.svg", "C:\\work\\a.svg", "/home/u/a.png"]) expect(isLocalPath(p), p).toBe(true);
    for (const p of ["https://a.com/x.svg", "data:image/png;base64,x", "#top", "mailto:a@b", "javascript:alert(1)", "//cdn/x.png", ""]) expect(isLocalPath(p), p).toBe(false);
  });
  it("解码中文路径、去掉 ./ 和查询串", () => {
    expect(normalizeLocalPath("./%E6%95%88%E6%9E%9C%E5%9B%BE.svg?x=1")).toBe("效果图.svg");
    expect(normalizeLocalPath("bad%E6.svg")).toBe("bad%E6.svg");
    expect(IMAGE_EXT.test("a.JPG") && !IMAGE_EXT.test("a.svg.txt")).toBe(true);
  });
  it("SVG 以 data URL 显示", () => {
    expect(svgDataURL("<svg/>")).toBe("data:image/svg+xml;charset=utf-8,%3Csvg%2F%3E");
  });
  it("识别只有小标题的思考摘要", () => {
    expect(isTitleOnly("**Preparing an SVG mockup**")).toBe(true);
    expect(isTitleOnly("**Plan**\n\nFirst I will…")).toBe(false);
  });
});
