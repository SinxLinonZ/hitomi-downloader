import { test, expect, Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

const svg = (label: string, color: string) =>
  `<svg xmlns="http://www.w3.org/2000/svg" width="700" height="1000"><rect width="700" height="1000" fill="${color}"/><text x="350" y="500" font-size="120" fill="#fff" text-anchor="middle">${label}</text></svg>`;
const entry = (id: number, extra: Record<string, unknown> = {}) => ({
  id,
  title: `Work ${id}`,
  artists: ["Someone"],
  language: "japanese",
  type: "manga",
  pages: 12,
  tags: [{ name: "glasses", namespace: "female" }],
  ...extra,
});

// ビルド済みの閲覧ページを、API のモックと一緒に読み込む。
async function setup(page: Page) {
  const requests: URL[] = [];
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("http://hitomi.test/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.startsWith("/api/v1/reader/")) requests.push(url);
    if (path === "/api/v1/reader/library") {
      const q = url.searchParams.get("q") || "";
      const tags = url.searchParams.getAll("tag");
      let entries = [
        entry(1, { title: "Alpha", japaneseTitle: "アルファ" }),
        entry(2, { title: "Beta", artists: [] }),
        entry(3, { title: "Gamma" }),
      ];
      if (q) entries = entries.filter((e) => e.title.toLowerCase().includes(q.toLowerCase()));
      if (tags.length) entries = entries.slice(0, 1);
      await route.fulfill({
        json: {
          entries,
          tags: [
            { value: "female:glasses", count: 3 },
            { value: "tag:digital", count: 2 },
          ],
          total: entries.length,
          page: 1,
          pageSize: 30,
          scanning: false,
          found: 3,
        },
      });
      return;
    }
    const work = path.match(/^\/api\/v1\/reader\/works\/(\d+)$/);
    if (work) {
      await route.fulfill({
        json: { ...entry(Number(work[1])), pageList: Array.from({ length: 12 }, () => ({ w: 700, h: 1000 })) },
      });
      return;
    }
    const pageImg = path.match(/^\/api\/v1\/reader\/works\/\d+\/pages\/(\d+)$/);
    if (pageImg) {
      await route.fulfill({ contentType: "image/svg+xml", body: svg(`P${pageImg[1]}`, "#334155") });
      return;
    }
    if (path.endsWith("/cover")) {
      await route.fulfill({ contentType: "image/svg+xml", body: svg("C", "#475569") });
      return;
    }
    if (path === "/favicon.ico") {
      await route.fulfill({ status: 404 });
      return;
    }
    const asset = resolve("dist", path === "/reader" ? "reader.html" : path.slice(1));
    await route.fulfill({
      contentType: asset.endsWith(".js") ? "application/javascript" : asset.endsWith(".css") ? "text/css" : "text/html",
      body: await readFile(asset),
    });
  });
  return { requests, errors };
}

test("列表显示作品，搜索和标签筛选会重新查询", async ({ page }) => {
  const { requests, errors } = await setup(page);
  await page.goto("http://hitomi.test/reader");
  await expect(page.getByRole("button", { name: "アルファ" })).toBeVisible();
  await expect(page.getByText("共 3 部")).toBeVisible();
  await expect(page.getByRole("button", { name: "Beta" })).toBeVisible();

  await page.getByLabel("搜索作品").fill("gam");
  await expect(page.getByRole("button", { name: "Gamma" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Beta" })).toHaveCount(0);
  expect(requests.some((u) => u.searchParams.get("q") === "gam")).toBe(true);
  await page.getByLabel("清除搜索").click();
  await expect(page.getByRole("button", { name: "Beta" })).toBeVisible();

  await page.getByRole("button", { name: "筛选" }).click();
  await page.getByRole("button", { name: /^female:glasses/ }).click();
  await expect(page.getByRole("button", { name: "查看 1 部作品" })).toBeVisible();
  expect(requests.some((u) => u.searchParams.getAll("tag").includes("female:glasses"))).toBe(true);
  await page.getByRole("button", { name: "查看 1 部作品" }).click();
  await expect(page.getByRole("button", { name: "Beta" })).toHaveCount(0);
  // 手机上不应出现横向滚动。
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(errors).toEqual([]);
});

test("滚动阅读：显示页码、可跳页，并在重新打开时恢复位置", async ({ page }) => {
  const { errors } = await setup(page);
  await page.goto("http://hitomi.test/reader");
  await page.getByRole("button", { name: "アルファ" }).click();
  const viewer = page.getByRole("dialog", { name: "阅览" });
  await expect(viewer).toBeVisible();
  await expect(viewer.getByText("1 / 12")).toBeVisible();
  await expect(viewer.getByRole("img", { name: "第 1 页" })).toBeVisible();
  // 现在页前后之外的页面不加载图片，避免占用过多内存。
  await expect(viewer.getByRole("img", { name: "第 12 页" })).toHaveCount(0);

  await viewer.locator(".v-scroll").evaluate((el) => (el.scrollTop = el.querySelector<HTMLElement>('[data-page="6"]')!.offsetTop + 10));
  await expect(viewer.getByText("6 / 12")).toBeVisible();
  await expect(viewer.getByRole("img", { name: "第 6 页" })).toBeVisible();
  await page.waitForTimeout(600); // 位置保存有短暂的延迟

  // 滚动后工具栏会自动隐藏，点一下屏幕再显示。
  await expect(viewer.locator(".v-top.v-hidden")).toHaveCount(1);
  await page.mouse.click(195, 420);
  await expect(viewer.locator(".v-top.v-hidden")).toHaveCount(0);
  await viewer.getByRole("button", { name: "返回列表" }).click();
  await expect(viewer).toHaveCount(0);
  await expect(page.getByText("读到 6/12")).toBeVisible();

  await page.getByRole("button", { name: "アルファ" }).click();
  await expect(page.getByRole("dialog", { name: "阅览" }).getByText("6 / 12")).toBeVisible();
  expect(errors).toEqual([]);
});

test("翻页模式：点击左右翻页，可切换右开", async ({ page }) => {
  const { errors } = await setup(page);
  await page.goto("http://hitomi.test/reader");
  await page.getByRole("button", { name: "Beta" }).click();
  const viewer = page.getByRole("dialog", { name: "阅览" });
  await viewer.getByRole("button", { name: "切换为翻页模式" }).click();
  await expect(viewer.getByRole("img", { name: "第 1 页" })).toBeVisible();
  // 左开（默认）：点右侧 = 下一页，点左侧 = 上一页。
  await page.mouse.click(350, 420);
  await expect(viewer.getByText("2 / 12")).toBeVisible();
  await page.mouse.click(40, 420);
  await expect(viewer.getByText("1 / 12")).toBeVisible();
  // 右开：点左侧 = 下一页。
  await viewer.getByRole("button", { name: /右开|左开/ }).click();
  await page.mouse.click(40, 420);
  await expect(viewer.getByText("2 / 12")).toBeVisible();
  // 中间区域切换工具栏。
  await page.mouse.click(195, 420);
  await expect(viewer.locator(".v-bottom.v-hidden")).toHaveCount(1);
  await page.mouse.click(195, 420);
  await expect(viewer.locator(".v-bottom.v-hidden")).toHaveCount(0);
  expect(errors).toEqual([]);
});
