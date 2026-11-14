import { test, expect } from "@playwright/test";
import { spawn, ChildProcess } from "node:child_process";
import { mkdtemp, mkdir, writeFile, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createHash } from "node:crypto";
let server: ChildProcess;
let startup: string;
let root: string;
let galleryDir: string;
let serverStderr = "";
test.beforeAll(async () => {
  root = await mkdtemp(join(tmpdir(), "hitomi-e2e-"));
  galleryDir = join(root, "downloads", "42 - Sample");
  await mkdir(galleryDir, { recursive: true });
  const png = Buffer.from(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aF9sAAAAASUVORK5CYII=",
    "base64",
  );
  await writeFile(join(galleryDir, "0001.png"), png);
  await writeFile(
    join(galleryDir, "metadata.json"),
    JSON.stringify({
      schemaVersion: 1,
      gallery: {
        id: 42,
        title: "离线校验示例",
        type: "manga",
        files: [{ name: "source.png", hash: "a".repeat(64) }],
      },
      pages: [
        {
          index: 0,
          name: "0001.png",
          sourceHash: "a".repeat(64),
          format: "png",
          bytes: png.length,
          sha256: createHash("sha256").update(png).digest("hex"),
        },
      ],
    }),
  );
  server = spawn(resolve("../hitomi-downloader"), [
    "--no-open",
    "--lang",
    "zh",
    // 验证会尝试联网对比上游；测试中指向不可用的 proxy，避免访问真实站点。
    "--proxy",
    "http://127.0.0.1:1",
    "--data-dir",
    join(root, "data"),
    "--download-dir",
    join(root, "downloads"),
  ]);
  startup = await new Promise((resolve, reject) => {
    let output = "";
    let stderr = "";
    server.stderr!.on("data", (b) => {
      stderr += b.toString();
      serverStderr += b.toString();
    });
    server.stdout!.on("data", (b) => {
      output += b.toString();
      const m = output.match(/http:\/\/127\.0\.0\.1:\d+\/\?token=[a-f0-9]+/);
      if (m) resolve(m[0]);
    });
    server.on("error", reject);
    server.on("exit", (code) =>
      reject(new Error("server exited " + code + ": " + stderr)),
    );
    setTimeout(() => reject(new Error("startup timeout")), 10000).unref();
  });
});
test.afterAll(async () => {
  server?.kill("SIGTERM");
});
test("扫描已下载并离线完成验证，保留原有图片", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(startup);
  await expect(page.getByText("本地服务已连接")).toBeVisible();
  await page.getByRole("button", { name: "已下载", exact: true }).click();
  await expect(page.getByText("离线校验示例")).toBeVisible();
  await page.getByRole("button", { name: "验证全部" }).click();
  await page.getByRole("dialog", { name: "验证全部" }).getByRole("button", { name: /^验证 \d+ 个作品/ }).click();
  await expect(page.getByText("已完成", { exact: true })).toBeVisible();
  await expect(page.getByText("1 / 1 页")).toBeVisible();
  expect(await readFile(join(galleryDir, "0001.png"))).toBeTruthy();
  expect(errors).toEqual([]);
  await page.screenshot({ path: join(root, "downloads.png"), fullPage: true });
});
test("搜索保留正常作品，并显示失败卡片与恢复操作", async ({ page }) => {
  await page.goto(startup);
  await page.route("**/api/v1/search?*", (route) =>
    route.fulfill({
      json: {
        session: "test",
        page: 1,
        total: 2,
        items: [
          {
            id: 7,
            gallery: {
              id: 7,
              title: "测试作品",
              japanese_title: "原文の作品名",
              language: "japanese",
              type: "manga",
              files: [{ name: "p.png", width: 100, height: 100 }],
            },
          },
          { id: 8, error: "获取信息超时，可重试" },
        ],
      },
    }),
  );
  await page.route("**/api/v1/covers/**", (route) => route.abort());
  await page.route("**/api/v1/galleries/7/related", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: 9,
            gallery: {
              id: 9,
              title: "Related Work",
              japanese_title: "関連作品のタイトル",
              language: "japanese",
              type: "manga",
              files: [{ name: "p.png", width: 100, height: 100 }],
            },
          },
          { id: 10, error: "获取信息超时" },
        ],
      },
    }),
  );
  await page.route("**/api/v1/galleries/9/related", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.getByLabel("关键词与标签").fill("test");
  await page.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(page.locator(".gallery-title")).toHaveText("原文の作品名");
  await expect(page.getByText("获取信息超时，可重试")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "重试此页信息" }),
  ).toBeVisible();
  // 封面、标题、详情按钮都能打开详情。
  await page.locator(".g-card .cover").first().click();
  await expect(page.getByRole("dialog", { name: "作品详情" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
  await page.getByRole("button", { name: "查看详情", exact: true }).click();
  const details = page.getByRole("dialog", { name: "作品详情" });
  await expect(
    details.getByRole("heading", { name: "原文の作品名", exact: true }),
  ).toBeVisible();
  await expect(details.locator(".work-title-secondary")).toHaveText("测试作品");
  // 関連作品：一覧に表示され、開くと詳細が切り替わり、戻れる。
  const related = details.getByRole("region", { name: "相关作品" });
  await expect(related.getByText("相关作品（2）")).toBeVisible();
  await expect(related.getByText("获取信息超时")).toBeVisible();
  await related.locator(".gallery-title").click();
  await expect(
    details.getByRole("heading", { name: "関連作品のタイトル", exact: true }),
  ).toBeVisible();
  await expect(details.getByText("没有相关作品。")).toBeVisible();
  await details.getByRole("button", { name: "返回上一个作品" }).click();
  await expect(
    details.getByRole("heading", { name: "原文の作品名", exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await page.screenshot({ path: join(root, "search.png"), fullPage: true });
});
test("设置下载目录后扫描新目录", async ({ page }) => {
  const newDir = join(root, "new-downloads");
  await page.goto(startup);
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByLabel("下载目录", { exact: true }).fill(newDir);
  // 并发数的默认值来自服务端，可修改并持久化。
  await expect(page.getByLabel("同时处理的任务数")).toHaveValue("2");
  await expect(page.getByLabel("每个任务的并发文件数")).toHaveValue("4");
  await page.getByLabel("同时处理的任务数").fill("3");
  await page.getByLabel("每个任务的并发文件数").fill("6");
  await page.getByRole("button", { name: "保存设置" }).click();
  await page.getByRole("button", { name: "已下载", exact: true }).click();
  await expect(
    page.getByText("没有发现已下载作品。可在设置中选择已有下载目录。"),
  ).toBeVisible();
  const saved = JSON.parse(
    await readFile(join(root, "data", "config.json"), "utf8"),
  );
  expect(saved.downloadDir).toBe(newDir);
  expect(saved.taskConcurrency).toBe(3);
  expect(saved.pageConcurrency).toBe(6);
});

test("移动端侧栏可打开并在切换页面后收起", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(startup);
  await page.getByRole("button", { name: "打开导航" }).click();
  await page.getByRole("button", { name: "已下载", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "已下载", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "打开导航" })).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});

test("停止服务按钮关闭进程并停止页面重连", async ({ page }) => {
  await page.goto(startup);
  const other = await page.context().newPage();
  await other.goto(startup);
  await expect(page.getByText("本地服务已连接")).toBeVisible();
  await expect(other.getByText("本地服务已连接")).toBeVisible();
  let reconnects = 0;
  page.on("request", (r) => {
    if (r.url().includes("/api/v1/events")) reconnects++;
  });
  const exited = new Promise<number | null>((resolve) =>
    server.once("exit", (code) => resolve(code)),
  );
  await page.getByRole("button", { name: "停止服务", exact: true }).click();
  await expect(
    page.getByRole("alert").filter({ hasText: "服务已停止" }),
  ).toBeVisible();
  await expect(
    other.getByRole("alert").filter({ hasText: "服务已停止" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "停止服务", exact: true }),
  ).toBeDisabled();
  expect(await exited).toBe(0);
  expect(serverStderr).not.toContain("context deadline exceeded");
  await page.waitForTimeout(1200);
  expect(reconnects).toBe(0);
  await other.close();
});
