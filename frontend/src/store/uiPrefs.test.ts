import { describe, expect, it } from "vitest";
import { DEFAULT_PREFS, normalizePrefs, resolveLanguage } from "./uiPrefs";

describe("normalizePrefs", () => {
  it("falls back to defaults for missing or unknown values", () => {
    expect(normalizePrefs(null)).toEqual(DEFAULT_PREFS);
    expect(normalizePrefs({ theme: "purple", fontSize: "large", extra: 1 })).toEqual({ ...DEFAULT_PREFS, fontSize: "large" });
  });

  it("resolves auto language from the browser", () => {
    expect(resolveLanguage("auto", "zh-CN")).toBe("zh");
    expect(resolveLanguage("auto", "en-US")).toBe("en");
    expect(resolveLanguage("en", "zh-CN")).toBe("en");
  });
});
