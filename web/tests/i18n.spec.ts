import { test, expect } from "@playwright/test";
import { readFile, readdir } from "node:fs/promises";
import { resolve, join } from "node:path";
import { ja } from "../src/locales/ja";
import { en } from "../src/locales/en";

// 画面の文言：ソース中の t("…") / tr("…") / tx("…") の文字列リテラルを集める。
async function sourceKeys() {
  const keys = new Set<string>();
  const dirs = [resolve("src"), resolve("src/reader")];
  for (const dir of dirs) {
    for (const name of await readdir(dir)) {
      if (!/\.tsx?$/.test(name)) continue;
      const text = await readFile(join(dir, name), "utf8");
      for (const m of text.matchAll(/(?<![\w.])(?:t|tr|tx)\(\s*("(?:[^"\\\n]|\\.)*")/g)) keys.add(JSON.parse(m[1]));
    }
  }
  return keys;
}
const placeholders = (s: string) => (s.match(/\{\w+\}/g) || []).sort().join(",");

test("すべての文言に日本語と英語の翻訳があり、置き換え項目も一致する", async () => {
  const keys = await sourceKeys();
  expect(keys.size).toBeGreaterThan(150);
  for (const [name, catalog] of [["ja", ja], ["en", en]] as const) {
    const missing = [...keys].filter((k) => !(k in catalog));
    expect(missing, `${name}: missing translations`).toEqual([]);
    const unused = Object.keys(catalog).filter((k) => !keys.has(k));
    expect(unused, `${name}: unused translations`).toEqual([]);
    const mismatched = [...keys].filter((k) => placeholders(k) !== placeholders(catalog[k] ?? ""));
    expect(mismatched, `${name}: placeholders differ`).toEqual([]);
  }
});

// ビルド済みの画面を、モックした API と一緒に読み込む。
async function open(page: import("@playwright/test").Page, settings: Record<string, unknown> = {}) {
  const puts: unknown[] = [];
  await page.route("http://hitomi.test/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/events") {
      await route.fulfill({ contentType: "text/event-stream", body: "data: []\n\n" });
    } else if (path === "/api/v1/settings") {
      if (route.request().method() === "PUT") puts.push(route.request().postDataJSON());
      await route.fulfill({ json: { downloadDir: "/downloads", effectiveLanguage: "en", ...settings } });
    } else if (path === "/api/v1/mobile-link") {
      await route.fulfill({ json: { lan: false, urls: [], localUrl: "http://127.0.0.1/reader" } });
    } else if (path === "/api/v1/reader/library") {
      await route.fulfill({ json: { entries: [], tags: [], total: 0, page: 1, pageSize: 30, scanning: false, found: 0 } });
    } else if (path === "/favicon.ico") {
      await route.fulfill({ status: 404 });
    } else {
      const asset = resolve("dist", path === "/" ? "index.html" : path === "/reader" ? "reader.html" : path.slice(1));
      await route.fulfill({
        contentType: asset.endsWith(".js") ? "application/javascript" : asset.endsWith(".css") ? "text/css" : "text/html",
        body: await readFile(asset),
      });
    }
  });
  return puts;
}

test.describe("日本語のブラウザ", () => {
  test.use({ locale: "ja-JP" });
  test("画面が日本語で表示される", async ({ page }) => {
    await open(page);
    await page.goto("http://hitomi.test/");
    await expect(page.getByRole("button", { name: "サービス停止" })).toBeVisible();
    await expect(page.getByRole("button", { name: "ダウンロードキュー" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "ja");
  });
});

test.describe("英語のブラウザ", () => {
  test.use({ locale: "en-US" });
  test("画面が英語で表示される", async ({ page }) => {
    await open(page);
    await page.goto("http://hitomi.test/");
    await expect(page.getByRole("button", { name: "Stop service" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Download queue" })).toBeVisible();
  });
  test("手机阅读页も英語で表示される", async ({ page }) => {
    await open(page);
    await page.goto("http://hitomi.test/reader");
    await expect(page.getByLabel("Search works")).toBeVisible();
    await expect(page.getByText("No works found")).toBeVisible();
  });
});

test("設定で言語を切り替えると、サーバーにも送られ、保存されて再読み込み後も維持される", async ({ page }) => {
  const puts = await open(page);
  await page.goto("http://hitomi.test/");
  await page.getByRole("button", { name: "设置" }).click();
  const dialog = page.getByRole("dialog", { name: "设置" });
  await dialog.getByRole("textbox", { name: "界面语言" }).click();
  await page.getByRole("option", { name: "日本語" }).click();
  await dialog.getByRole("button", { name: "保存设置" }).click();
  await expect(page.getByRole("button", { name: "サービス停止" })).toBeVisible();
  expect(puts).toEqual([expect.objectContaining({ language: "ja" })]);
  await page.reload();
  await expect(page.getByRole("button", { name: "サービス停止" })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("hitomi.lang"))).toBe("ja");
  // 「自動」に戻すとブラウザの言語（zh-CN）に従う。
  await page.getByRole("button", { name: "設定" }).click();
  await page.getByRole("dialog", { name: "設定" }).getByRole("textbox", { name: "表示言語" }).click();
  await page.getByRole("option", { name: "自動" }).click();
  await page.getByRole("dialog", { name: "設定" }).getByRole("button", { name: "設定を保存" }).click();
  await expect(page.getByRole("button", { name: "停止服务" })).toBeVisible();
  expect(puts.at(-1)).toEqual(expect.objectContaining({ language: "auto" }));
});
