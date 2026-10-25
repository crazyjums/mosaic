import { describe, expect, it } from "vitest";
import { checkAdd, classify, fmtSize, LIMITS } from "./attachments";

describe("附件类型", () => {
  it("图片：PNG / JPEG / GIF / WebP", () => {
    for (const t of ["image/png", "image/jpeg", "image/gif", "image/webp"]) expect(classify("a", t)).toBe("image");
  });
  it("其他图片格式明确拒绝", () => {
    expect(classify("a.heic", "image/heic")).toBe("unsupported");
    expect(classify("a.bmp", "image/bmp")).toBe("unsupported");
  });
  it("SVG 是文本，当作代码附件", () => {
    expect(classify("logo.svg", "image/svg+xml")).toBe("text");
    expect(classify("logo.svg", "")).toBe("text");
  });
  it("Markdown / 纯文本 / 代码 / 配置", () => {
    for (const n of ["README.md", "notes.txt", "a.log", "main.go", "x.tsx", "conf.yaml", "data.csv", "Makefile", "LICENSE", "Dockerfile"]) expect(classify(n, ""), n).toBe("text");
    expect(classify("x", "text/plain")).toBe("text");
    expect(classify("x.json", "application/json")).toBe("text");
  });
  it("PDF、Office、压缩包、可执行文件暂不支持", () => {
    for (const n of ["a.pdf", "a.docx", "a.xlsx", "a.zip", "a.exe", "a.png.bak", "noext"]) expect(classify(n, ""), n).toBe("unsupported");
  });
});

describe("附件限制", () => {
  it("数量上限", () => {
    const many = Array.from({ length: LIMITS.count }, () => ({ kind: "text" as const, size: 1 }));
    expect(checkAdd(many, { kind: "text", size: 1 })).toMatch(/最多/);
    expect(checkAdd(many.slice(1), { kind: "image", size: 1 })).toBeNull();
  });
  it("文本附件合计上限，图片不计入", () => {
    const a = [{ kind: "text" as const, size: LIMITS.totalTextBytes - 10 }, { kind: "image" as const, size: LIMITS.imageBytes }];
    expect(checkAdd(a, { kind: "text", size: 11 })).toMatch(/合计/);
    expect(checkAdd(a, { kind: "text", size: 10 })).toBeNull();
    expect(checkAdd(a, { kind: "image", size: 100 })).toBeNull();
  });
  it("大小显示", () => {
    expect(fmtSize(500)).toBe("500 B");
    expect(fmtSize(2048)).toBe("2.0 KB");
    expect(fmtSize(300 * 1024)).toBe("300 KB");
    expect(fmtSize(5 * 1024 * 1024)).toBe("5.0 MB");
  });
});
