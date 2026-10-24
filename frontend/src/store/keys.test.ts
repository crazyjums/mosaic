import { describe, expect, it } from "vitest";
import { isSendKey } from "./keys";

const key = (k: Partial<Parameters<typeof isSendKey>[0]>) => ({ key: "Enter", shiftKey: false, ctrlKey: false, metaKey: false, isComposing: false, ...k });

describe("isSendKey", () => {
  it("enter mode", () => {
    expect(isSendKey(key({}), "enter")).toBe(true);
    expect(isSendKey(key({ shiftKey: true }), "enter")).toBe(false);
    expect(isSendKey(key({ isComposing: true }), "enter")).toBe(false);
    expect(isSendKey(key({ key: "a" }), "enter")).toBe(false);
  });

  it("mod+enter mode", () => {
    expect(isSendKey(key({}), "mod_enter")).toBe(false);
    expect(isSendKey(key({ ctrlKey: true }), "mod_enter")).toBe(true);
    expect(isSendKey(key({ metaKey: true }), "mod_enter")).toBe(true);
    expect(isSendKey(key({ metaKey: true, isComposing: true }), "mod_enter")).toBe(false);
  });
});
