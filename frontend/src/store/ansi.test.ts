import { describe, expect, it } from "vitest";
import { parseAnsi } from "./ansi";

describe("parseAnsi", () => {
  it("plain text", () => {
    expect(parseAnsi("hello")).toEqual([{ text: "hello" }]);
  });

  it("basic colors, bold and reset", () => {
    const segs = parseAnsi("\x1b[1;32mok\x1b[0m done");
    expect(segs).toEqual([{ text: "ok", bold: true, fg: "#22c55e" }, { text: " done" }]);
  });

  it("256 and true color", () => {
    const segs = parseAnsi("\x1b[38;5;196ma\x1b[48;2;1;2;3mb\x1b[39mc");
    expect(segs[0]).toEqual({ text: "a", fg: "rgb(255,0,0)" });
    expect(segs[1]).toEqual({ text: "b", fg: "rgb(255,0,0)", bg: "rgb(1,2,3)" });
    expect(segs[2]).toEqual({ text: "c", bg: "rgb(1,2,3)" });
  });

  it("drops other control sequences", () => {
    expect(parseAnsi("\x1b[2Kx\x07y")).toEqual([{ text: "xy" }]);
  });
});
