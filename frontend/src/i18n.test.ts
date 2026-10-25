import { afterEach, describe, expect, it } from "vitest";
import { EN, setLanguage, t } from "./i18n";

// 这些文件里的每个 t("…") 都必须有英文翻译；新增界面文字时漏了翻译，这个测试会指出是哪一句。
const sources = import.meta.glob(
  [
    "./App.tsx",
    "./components/{Sidebar,ContextPanel,MessageList,PermissionDialog,ToolCallCard,ThinkingCard,AssistantMessage,Composer,StatusBar,PermissionModeSelect}.tsx",
    "./components/{SettingsDialog,ChatGPTAccount,AppearanceSettings,SkinSettings,PermissionsSettings,MCPSettings,GeneralSettings,StatusLineSettings}.tsx",
  ],
  { query: "?raw", import: "default", eager: true },
) as Record<string, string>;

describe("i18n", () => {
  afterEach(() => setLanguage("zh"));

  it("translates and interpolates", () => {
    expect(t("发送")).toBe("发送");
    setLanguage("en");
    expect(t("发送")).toBe("Send");
    expect(t(" · 第 {n} 步", { n: 3 })).toBe(" · step 3");
    expect(t("没有翻译的句子")).toBe("没有翻译的句子");
  });

  it("every t() literal has an English translation", () => {
    expect(Object.keys(sources).length).toBeGreaterThan(10);
    const missing: string[] = [];
    for (const [file, src] of Object.entries(sources)) {
      for (const m of src.matchAll(/\bt\(\s*"((?:[^"\\]|\\.)*)"/g)) {
        const key = JSON.parse(`"${m[1]}"`) as string;
        if (!(key in EN)) missing.push(`${file}: ${key}`);
      }
    }
    expect(missing).toEqual([]);
  });
});
