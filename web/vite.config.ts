import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { writeFileSync } from "node:fs";
export default defineConfig({
  plugins: [
    react(),
    {
      // ビルドは dist を空にするが、ビルド前でも go build できるよう .gitkeep を残す。
      name: "keep-dist-placeholder",
      closeBundle() {
        writeFileSync("dist/.gitkeep", "");
      },
    },
  ],
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
