import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  workers: 1,
  use: {
    headless: true,
    // 既存のテストは簡体字中国語の画面を前提にする（言語切り替えは i18n.spec.ts で確認）。
    locale: "zh-CN",
    launchOptions: {
      executablePath:
        process.env.CHROME_PATH ||
        "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
      args: ["--no-sandbox"],
    },
  },
  reporter: "list",
});
