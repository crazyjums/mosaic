import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Wails 开发模式（wails dev）会启动这里的 Vite 开发服务器并把页面嵌进桌面窗口；
// 单独 `npm run dev` 时在浏览器里打开，前端会自动切换到演示桥接（见 src/bridge/index.ts）。
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: { port: 5173, strictPort: true },
});
