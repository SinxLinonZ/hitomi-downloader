import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  // 管理画面（index.html）と手机向けの阅读页（reader.html）の 2 ページ構成。
  build: {
    rollupOptions: {
      input: { main: "index.html", reader: "reader.html" },
    },
  },
  server: {
    proxy: {
      "/api": {
        target: process.env.HITOMI_BACKEND || "http://127.0.0.1:8080",
        changeOrigin: false,
      },
    },
  },
});
