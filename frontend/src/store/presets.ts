// 新建厂商时可选的预设：只是帮用户填好协议类型和 Base URL，所有字段都可以再改。
//
// 模型列表大多留空：各家模型更新很快，写死容易过时；填好密钥后点"获取模型列表"从服务器拉取最准确。

export interface Preset {
  id: string;
  label: string;
  name: string; // 默认的配置档名称
  type: "openai" | "anthropic";
  baseURL: string;
  models: string[];
  options?: Record<string, unknown>;
  /** 显示在表单下方的小提示 */
  hint?: string;
}

export const PRESETS: Preset[] = [
  {
    id: "custom", label: "自定义（OpenAI 兼容）", name: "", type: "openai", baseURL: "", models: [],
    hint: "任何兼容 OpenAI Chat Completions 接口的服务都可以：填 Base URL（通常以 /v1 结尾）和 API Key。",
  },
  {
    id: "openai", label: "OpenAI", name: "openai", type: "openai", baseURL: "https://api.openai.com/v1", models: [],
    options: { max_tokens_field: "max_completion_tokens" },
    hint: "使用 OpenAI 开发者平台（platform.openai.com）的 API Key，按量计费。",
  },
  {
    id: "anthropic", label: "Anthropic（Claude）", name: "anthropic", type: "anthropic", baseURL: "https://api.anthropic.com",
    models: ["claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"],
    options: { thinking: "adaptive" },
    hint: "使用 Anthropic Console 的 API Key。原生协议，支持显示思考过程。",
  },
  { id: "deepseek", label: "DeepSeek", name: "deepseek", type: "openai", baseURL: "https://api.deepseek.com/v1", models: ["deepseek-chat", "deepseek-reasoner"] },
  { id: "qwen", label: "通义千问（阿里云百炼）", name: "qwen", type: "openai", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", models: [] },
  { id: "moonshot", label: "Moonshot / Kimi", name: "moonshot", type: "openai", baseURL: "https://api.moonshot.cn/v1", models: [] },
  { id: "zhipu", label: "智谱 GLM", name: "zhipu", type: "openai", baseURL: "https://open.bigmodel.cn/api/paas/v4", models: [] },
  { id: "siliconflow", label: "硅基流动 SiliconFlow", name: "siliconflow", type: "openai", baseURL: "https://api.siliconflow.cn/v1", models: [] },
  { id: "openrouter", label: "OpenRouter", name: "openrouter", type: "openai", baseURL: "https://openrouter.ai/api/v1", models: [] },
  {
    id: "ollama", label: "Ollama（本地）", name: "ollama", type: "openai", baseURL: "http://localhost:11434/v1", models: [],
    hint: "本地模型不需要 API Key，留空即可。先用 ollama pull 下载模型。",
  },
];
