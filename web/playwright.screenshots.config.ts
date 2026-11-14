import { defineConfig } from "@playwright/test";
// README 用のスクリーンショットを作る設定（通常のテストとは別。npm run screenshots）。
export default defineConfig({
  testDir: "./screenshots",
  workers: 1,
  use: {
    headless: true,
    launchOptions: {
      executablePath: process.env.CHROME_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
      args: ["--no-sandbox"],
    },
  },
  reporter: "list",
});
