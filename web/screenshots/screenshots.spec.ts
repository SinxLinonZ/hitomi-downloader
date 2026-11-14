import { test, expect, Page } from "@playwright/test";
import { readFile, mkdir } from "node:fs/promises";
import { resolve } from "node:path";

// すべて架空の作品とモックの API。実在の作品や画像は使わない。
const OUT = resolve("../docs/screenshots");

type Work = { id: number; title: string; jp?: string; artist: string; pages: number; tags: [string, string][]; lang?: string };
const WORKS: Work[] = [
  { id: 100101, title: "Starlight Café", jp: "星明かりのカフェ", artist: "Mio Tanaka", pages: 32, tags: [["tag", "slice_of_life"], ["tag", "cafe"]] },
  { id: 100102, title: "The Last Train to Nowhere", artist: "Sample Artist", pages: 48, tags: [["tag", "mystery"], ["tag", "train"]] },
  { id: 100103, title: "Cat Detective Agency", jp: "猫探偵事務所", artist: "Ren Aoki", pages: 24, tags: [["tag", "comedy"], ["tag", "animals"]] },
  { id: 100104, title: "Moonlit Library", artist: "Yuki Sato", pages: 56, tags: [["tag", "fantasy"], ["tag", "books"]] },
  { id: 100105, title: "Robot Gardener", jp: "ロボット庭師", artist: "Sample Artist", pages: 20, tags: [["tag", "sci-fi"], ["tag", "garden"]] },
  { id: 100106, title: "Summer Festival Days", artist: "Hana Mori", pages: 36, tags: [["tag", "slice_of_life"], ["tag", "summer"]] },
  { id: 100107, title: "Paper Plane Club", jp: "紙飛行機部", artist: "Kai Ito", pages: 28, tags: [["tag", "school"], ["tag", "club"]] },
  { id: 100108, title: "Dragon's Tea Shop", artist: "Mio Tanaka", pages: 40, tags: [["tag", "fantasy"], ["tag", "cafe"]] },
  { id: 100109, title: "Midnight Bakery", jp: "真夜中のパン屋", artist: "Hana Mori", pages: 30, tags: [["tag", "slice_of_life"], ["tag", "food"]] },
  { id: 100110, title: "Cloud Atlas Express", artist: "Ren Aoki", pages: 44, tags: [["tag", "adventure"], ["tag", "train"]] },
  { id: 100111, title: "Ocean Lantern", jp: "海のランタン", artist: "Yuki Sato", pages: 52, tags: [["tag", "fantasy"], ["tag", "sea"]] },
  { id: 100112, title: "The Quiet Observatory", artist: "Kai Ito", pages: 26, tags: [["tag", "sci-fi"], ["tag", "space"]] },
];
const byId = (id: number) => WORKS.find((w) => w.id === id) ?? WORKS[0];
const gallery = (w: Work) => ({
  id: w.id,
  title: w.title,
  japanese_title: w.jp,
  language: "japanese",
  type: "manga",
  files: Array.from({ length: w.pages }, () => ({ name: "p.webp", width: 800, height: 1130 })),
  artists: [{ artist: w.artist }],
  tags: w.tags.map(([, tag]) => ({ tag })),
});
const entry = (w: Work, i: number, extra: Record<string, unknown> = {}) => ({
  id: w.id,
  title: w.title,
  japaneseTitle: w.jp,
  artists: [w.artist],
  language: "japanese",
  type: "manga",
  pages: w.pages,
  directory: `/comics/${w.id} - ${w.title}`,
  legacy: false,
  formats: ["webp"],
  fallbacks: 0,
  missing: 0,
  tags: w.tags.map(([namespace, name]) => ({ namespace, name })),
  createdAt: new Date(Date.UTC(2026, 10, 14 - Math.floor(i / 3), 12 - i)).toISOString(),
  updatedAt: new Date(Date.UTC(2026, 10, 14 - Math.floor(i / 3), 12 - i)).toISOString(),
  ...extra,
});

// 架空の表紙・ページ（SVG）。作品 ID から色を決める。
function cover(id: number, w = 320, h = 452) {
  const hue = (id * 47) % 360;
  const w2 = byId(id);
  const sky = `hsl(${hue} 55% 38%)`, sea = `hsl(${(hue + 40) % 360} 50% 22%)`, glow = `hsl(${(hue + 160) % 360} 80% 70%)`;
  const title = w2.title.length > 18 ? w2.title.slice(0, 17) + "…" : w2.title;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 320 452">
  <defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${sky}"/><stop offset="1" stop-color="${sea}"/></linearGradient></defs>
  <rect width="320" height="452" fill="url(#g)"/>
  <circle cx="${90 + (id % 5) * 35}" cy="120" r="46" fill="${glow}" opacity=".85"/>
  <path d="M0 300 Q80 ${240 + (id % 4) * 12} 160 290 T320 280 V452 H0Z" fill="#000" opacity=".28"/>
  <path d="M0 350 Q90 310 180 345 T320 335 V452 H0Z" fill="#000" opacity=".28"/>
  <rect x="18" y="352" width="284" height="78" rx="8" fill="#000" opacity=".5"/>
  <text x="160" y="388" font-family="Helvetica, Arial, sans-serif" font-size="22" font-weight="700" fill="#fff" text-anchor="middle">${title.replace(/&/g, "&amp;")}</text>
  <text x="160" y="414" font-family="Helvetica, Arial, sans-serif" font-size="13" fill="#fff" opacity=".75" text-anchor="middle">SAMPLE · #${id}</text>
  </svg>`;
}
function pageArt(id: number, n: number) {
  const hue = (id * 47 + n * 13) % 360;
  const panel = (x: number, y: number, w: number, h: number, k: number) =>
    `<rect x="${x}" y="${y}" width="${w}" height="${h}" fill="hsl(${(hue + k * 30) % 360} 45% ${70 - k * 4}%)" stroke="#111" stroke-width="5"/>
     <circle cx="${x + w * 0.65}" cy="${y + h * 0.35}" r="${Math.min(w, h) * 0.16}" fill="#fff" opacity=".8"/>
     <path d="M${x} ${y + h} L${x + w * 0.4} ${y + h * 0.55} L${x + w} ${y + h}Z" fill="#000" opacity=".25"/>`;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="800" height="1130" viewBox="0 0 800 1130">
  <rect width="800" height="1130" fill="#fafafa"/>
  ${panel(30, 30, 740, 380, 0)}${panel(30, 440, 360, 320, 1)}${panel(410, 440, 360, 320, 2)}${panel(30, 790, 740, 310, 3)}
  <ellipse cx="560" cy="150" rx="150" ry="60" fill="#fff" stroke="#111" stroke-width="4"/>
  <text x="560" y="158" font-family="Helvetica, Arial, sans-serif" font-size="30" text-anchor="middle" fill="#111">Sample page ${n}</text>
  </svg>`;
}

const BASE_TASKS = [
  { id: 100103, title: "Cat Detective Agency", state: "running", operation: "download", directory: "", done: 17, total: 24 },
  { id: 100104, title: "Moonlit Library", state: "running", operation: "download", directory: "", done: 31, total: 56 },
  { id: 100110, title: "Cloud Atlas Express", state: "running", operation: "download", directory: "", done: 6, total: 44 },
  { id: 100108, title: "Dragon's Tea Shop", state: "queued", operation: "download", directory: "", done: 0, total: 40 },
  { id: 100111, title: "Ocean Lantern", state: "queued", operation: "download", directory: "", done: 0, total: 52 },
  { id: 100101, title: "Starlight Café", state: "completed", operation: "download", directory: "/comics/100101", done: 32, total: 32 },
  { id: 100102, title: "The Last Train to Nowhere", state: "completed", operation: "repair", directory: "/comics/100102", done: 48, total: 48 },
  { id: 100105, title: "Robot Gardener", state: "failed", operation: "download", directory: "", done: 12, total: 20, error: "upstream request failed (HTTP 503, attempt 4/4)" },
  { id: 100106, title: "Summer Festival Days", state: "paused", operation: "download", directory: "", done: 9, total: 36 },
].map((t) => ({ ...t, errors: {} }));
let TASKS = BASE_TASKS;

async function mock(page: Page) {
  // SSE は切断されると「接続中」の表示になるので、切れない偽の EventSource に差し替える。
  await page.addInitScript((tasks) => {
    class FakeEventSource {
      onmessage: ((e: { data: string }) => void) | null = null;
      onerror: (() => void) | null = null;
      constructor() {
        setTimeout(() => this.onmessage?.({ data: JSON.stringify(tasks) }), 0);
      }
      addEventListener() {}
      close() {}
    }
    (window as unknown as { EventSource: unknown }).EventSource = FakeEventSource;
  }, TASKS);
  await page.route("http://hitomi.test/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = (body: unknown) => route.fulfill({ json: body });
    const svg = (body: string) => route.fulfill({ contentType: "image/svg+xml", body });
    if (path === "/api/v1/settings") return json({ downloadDir: "/Users/you/comics", taskConcurrency: 2, pageConcurrency: 4, effectiveLanguage: "en" });
    if (path === "/api/v1/mobile-link") return json({ lan: false, urls: [], localUrl: "http://127.0.0.1/reader" });
    if (path === "/api/v1/downloads") return json(TASKS);
    if (path === "/api/v1/search") return json({ session: "s", page: 1, total: 1284, items: WORKS.map((w) => ({ id: w.id, gallery: gallery(w) })) });
    if (/^\/api\/v1\/galleries\/\d+\/related$/.test(path)) return json({ items: WORKS.slice(4, 10).map((w) => ({ id: w.id, gallery: gallery(w) })) });
    if (path === "/api/v1/library/downloaded") return json({ ids: [100101, 100102, 100107, 100109] });
    if (path === "/api/v1/library") {
      const entries = WORKS.map((w, i) =>
        entry(w, i, i === 2 ? { upstream: "changed", upstreamNote: i18nNote() } : i === 5 ? { legacy: true, formats: [] } : i === 7 ? { missing: 3 } : {}),
      );
      return json({ tags: [], entries, total: entries.length, legacyTotal: 1, page: 1, pageSize: 50, scan: { state: "completed", root: "/comics", visited: 912, found: 912, current: "", warnings: [], warningCount: 0 } });
    }
    const m = path.match(/^\/api\/v1\/covers\/(\d+)$/);
    if (m) return svg(cover(Number(m[1])));
    if (path === "/api/v1/library/cover") return svg(cover(Number(/\/comics\/(\d+)/.exec(url.searchParams.get("directory") || "")?.[1] ?? 100101)));
    if (path === "/api/v1/reader/library")
      return json({
        entries: WORKS.map((w) => ({ id: w.id, title: w.title, japaneseTitle: w.jp, artists: [w.artist], language: "japanese", type: "manga", pages: w.pages, tags: w.tags.map(([namespace, name]) => ({ namespace, name })) })),
        tags: [{ value: "tag:slice_of_life", count: 3 }, { value: "tag:fantasy", count: 3 }],
        total: WORKS.length, page: 1, pageSize: 30, scanning: false, found: 912,
      });
    const rw = path.match(/^\/api\/v1\/reader\/works\/(\d+)$/);
    if (rw) {
      const w = byId(Number(rw[1]));
      return json({ id: w.id, title: w.title, japaneseTitle: w.jp, artists: [w.artist], language: "japanese", type: "manga", pages: w.pages, tags: [], pageList: Array.from({ length: w.pages }, () => ({ w: 800, h: 1130 })) });
    }
    const rc = path.match(/^\/api\/v1\/reader\/works\/(\d+)\/cover$/);
    if (rc) return svg(cover(Number(rc[1])));
    const rp = path.match(/^\/api\/v1\/reader\/works\/(\d+)\/pages\/(\d+)$/);
    if (rp) return svg(pageArt(Number(rp[1]), Number(rp[2])));
    if (path === "/favicon.ico") return route.fulfill({ status: 404 });
    const asset = resolve("dist", path === "/" ? "index.html" : path === "/reader" ? "reader.html" : path.slice(1));
    return route.fulfill({
      contentType: asset.endsWith(".js") ? "application/javascript" : asset.endsWith(".css") ? "text/css" : "text/html",
      body: await readFile(asset),
    });
  });
}
let note = "";
const i18nNote = () => note;

const LANGS = [
  { code: "en", locale: "en-US", note: "2 pages replaced, 1 added", failure: "upstream request failed (HTTP 503, attempt 4/4)" },
  { code: "zh-CN", locale: "zh-CN", note: "替换 2 页，新增 1 页", failure: "上游请求失败（HTTP 503，尝试 4/4）" },
  { code: "ja", locale: "ja-JP", note: "2 ページ差し替え、1 ページ追加", failure: "上流へのリクエストに失敗しました（HTTP 503、試行 4/4）" },
];
// 失敗タスクの文言は、サーバーの言語に合わせる。
const withFailure = (message: string) => BASE_TASKS.map((t) => (t.error ? { ...t, error: message } : t));

for (const lang of LANGS) {
  test.describe(lang.code, () => {
    test.use({ locale: lang.locale });

    test("desktop", async ({ page }) => {
      note = lang.note;
      TASKS = withFailure(lang.failure);
      await mkdir(OUT, { recursive: true });
      await page.setViewportSize({ width: 1280, height: 800 });
      await mock(page);
      await page.goto("http://hitomi.test/");
      const nav = page.locator("button.workspace-nav");
      // 検索
      await page.locator("[data-hotkey-search]").first().fill("cafe");
      await page.locator('button[type="submit"]').first().click();
      await expect(page.locator(".g-card").first()).toBeVisible();
      await expect(page.locator(".g-card img").first()).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.screenshot({ path: `${OUT}/${lang.code}-search.png` });
      // 詳細
      await page.locator(".g-card .cover").nth(3).click();
      await expect(page.getByRole("dialog")).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.waitForTimeout(400);
      await page.screenshot({ path: `${OUT}/${lang.code}-detail.png` });
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog")).toBeHidden();
      // キュー
      await nav.nth(1).click();
      await expect(page.locator(".q-table")).toBeVisible();
      await page.waitForTimeout(300);
      await page.screenshot({ path: `${OUT}/${lang.code}-queue.png` });
      // ダウンロード済み
      await nav.nth(2).click();
      await expect(page.locator(".downloaded-row").first()).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.waitForTimeout(300);
      await page.screenshot({ path: `${OUT}/${lang.code}-downloaded.png` });
    });

    test("mobile", async ({ browser }) => {
      TASKS = withFailure(lang.failure);
      const context = await browser.newContext({ locale: lang.locale, viewport: { width: 390, height: 780 }, deviceScaleFactor: 2, hasTouch: true });
      const page = await context.newPage();
      await mock(page);
      await page.goto("http://hitomi.test/reader");
      await expect(page.locator(".r-card").first()).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.screenshot({ path: `${OUT}/${lang.code}-reader-list.png` });
      await page.locator(".r-card").nth(1).click();
      await expect(page.getByRole("dialog").locator("img").first()).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.waitForTimeout(300);
      await page.screenshot({ path: `${OUT}/${lang.code}-reader-page.png` });
      await context.close();
    });
  });
}
