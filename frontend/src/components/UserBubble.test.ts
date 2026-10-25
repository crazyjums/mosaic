import { describe, expect, it } from "vitest";
import { COLLAPSE_CHARS, COLLAPSE_LINES, shouldCollapse } from "./UserBubble";

describe("长输入折叠", () => {
  it("短消息不折叠", () => {
    expect(shouldCollapse("你好")).toBe(false);
    expect(shouldCollapse("a\n".repeat(COLLAPSE_LINES - 1))).toBe(false);
  });
  it("行数或字数超过阈值就折叠", () => {
    expect(shouldCollapse("a\n".repeat(COLLAPSE_LINES + 1))).toBe(true);
    expect(shouldCollapse("字".repeat(COLLAPSE_CHARS + 1))).toBe(true);
  });
});
