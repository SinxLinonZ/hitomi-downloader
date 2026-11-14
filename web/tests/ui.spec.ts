import { test, expect, Route } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";

// ビルド済み UI を request interception で読み込み、TCP ポートを使わずに検証する。
test("AppShell 导航和停止后的操作状态", async ({ page }) => {
  const streams: Route[] = [];
  const errors: string[] = [];
  let stopped = false;
  let eventRequests = 0;
  let migrated = false;
  const legacyDir = "/Users/example/Downloads/Hitomi/legacy";
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("http://hitomi.test/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/favicon.ico") {
      await route.fulfill({ status: 404 });
      return;
    }
    if (path === "/api/v1/events") {
      eventRequests++;
      if (stopped)
        await route.fulfill({
          contentType: "text/event-stream",
          body: "event: shutdown\ndata: {}\n\n",
        });
      else streams.push(route);
      return;
    }
    if (path === "/api/v1/shutdown") {
      stopped = true;
      await route.fulfill({ json: { ok: true } });
      await Promise.all(
        streams.map((stream) =>
          stream
            .fulfill({
              contentType: "text/event-stream",
              body: "event: shutdown\ndata: {}\n\n",
            })
            .catch(() => {}),
        ),
      );
      return;
    }
    if (path === "/api/v1/settings") {
      await route.fulfill({
        json: { downloadDir: "/Users/example/Downloads/Hitomi" },
      });
      return;
    }
    if (path === "/api/v1/library") {
      const entry = {
        id: 7,
        title: "旧格式示例",
        japaneseTitle: "日本語タイトル",
        artists: ["Test Artist"],
        tags: [
          { name: "digital", namespace: "tag" },
          { name: "glasses", namespace: "female" },
        ],
        language: "japanese",
        type: "manga",
        pages: 42,
        directory: legacyDir,
        legacy: !migrated,
        formats: migrated ? ["webp"] : [],
        fallbacks: 0,
        missing: 0,
      };
      await route.fulfill({
        json: {
          entries: [entry],
          tags: [
            { value: "tag:digital", count: 1 },
            { value: "female:glasses", count: 1 },
          ],
          total: 1,
          legacyTotal: migrated ? 0 : 1,
          page: 1,
          pageSize: 20,
          scan: {
            root: "/Users/example/Downloads/Hitomi",
            state: "completed",
            visited: 2,
            found: 1,
            current: legacyDir,
            warnings: [],
            warningCount: 0,
          },
        },
      });
      return;
    }
    if (path === "/api/v1/library/cover") {
      await route.fulfill({ status: 404 });
      return;
    }
    if (path === "/api/v1/library/migrate") {
      expect(route.request().postDataJSON().directories).toEqual([legacyDir]);
      migrated = true;
      await route.fulfill({ json: { accepted: [legacyDir], errors: {} } });
      return;
    }
    const asset = resolve("dist", path === "/" ? "index.html" : path.slice(1));
    const contentType = asset.endsWith(".js")
      ? "application/javascript"
      : asset.endsWith(".css")
        ? "text/css"
        : "text/html";
    await route.fulfill({ contentType, body: await readFile(asset) });
  });
  await page.goto("http://hitomi.test/");
  await expect(
    page.getByRole("heading", { name: "搜索作品", exact: true }),
  ).toBeVisible();
  expect(
    await page.locator("html").getAttribute("data-mantine-color-scheme"),
  ).toBe("dark");
  await page.getByRole("button", { name: "已下载", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "已下载", exact: true }),
  ).toBeVisible();
  await expect(
    page
      .locator(".downloaded-row")
      .getByRole("heading", { name: "日本語タイトル", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".downloaded-row .work-title-secondary"),
  ).toHaveText("旧格式示例");
  await expect(page.getByText("42 页", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: "搜索已下载", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", {
      name: "按标签 female:glasses 筛选",
      exact: true,
    }),
  ).toBeVisible();
  const filtered = page.waitForRequest((r) => {
    const u = new URL(r.url());
    return (
      u.pathname === "/api/v1/library" &&
      u.searchParams.getAll("tag").includes("female:glasses")
    );
  });
  await page
    .getByRole("button", { name: "按标签 female:glasses 筛选", exact: true })
    .click();
  await filtered;
  const searched = page.waitForRequest((r) => {
    const u = new URL(r.url());
    return (
      u.pathname === "/api/v1/library" &&
      u.searchParams.get("q") === "Test Artist -tag:missing"
    );
  });
  await page
    .getByRole("textbox", { name: "搜索已下载", exact: true })
    .fill("Test Artist -tag:missing");
  await searched;
  await page.getByRole("button", { name: "清除筛选", exact: true }).click();
  await expect(
    page.getByRole("img", { name: "日本語タイトル 封面" }),
  ).toBeVisible();
  expect(migrated).toBe(false);
  await page.getByRole("checkbox", { name: "选择作品 7", exact: true }).check();
  await page
    .getByRole("button", { name: "迁移所选（1）", exact: true })
    .click();
  await expect(
    page.locator(".downloaded-row").getByText("待迁移", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page
      .locator(".downloaded-row")
      .getByRole("button", { name: "验证并补全", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page.getByLabel("下载目录", { exact: true })).toHaveValue(
    "/Users/example/Downloads/Hitomi",
  );
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
  await page.screenshot({
    path: join(tmpdir(), "hitomi-appshell-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "打开导航" }).click();
  await page.getByRole("button", { name: "搜索作品", exact: true }).click();
  await expect(page.getByRole("button", { name: "打开导航" })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "搜索作品", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: join(tmpdir(), "hitomi-appshell-mobile.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "停止服务", exact: true }).click();
  await expect(
    page.getByRole("alert").filter({ hasText: "服务已停止" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "停止服务", exact: true }),
  ).toBeDisabled();
  expect(
    await page
      .locator(".workspace-content")
      .evaluate((el) => el.hasAttribute("inert")),
  ).toBe(true);
  const before = eventRequests;
  await page.waitForTimeout(1200);
  expect(eventRequests).toBe(before);
  expect(errors).toEqual([]);
});

test("全部迁移固定所有页的目标并分批入队", async ({ page }) => {
  const root = "/Users/example/Downloads/Hitomi";
  const directories = Array.from(
    { length: 205 },
    (_, i) => `${root}/legacy-${1000 + i}`,
  );
  const batches: string[][] = [];
  let scanning = true;
  let snapshots = 0;
  let remaining = 207;
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("http://hitomi.test/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path === "/api/v1/events") return;
    if (path === "/api/v1/settings") {
      await route.fulfill({ json: { downloadDir: root } });
      return;
    }
    if (path === "/api/v1/library") {
      const entries = directories.slice(0, 20).map((directory, i) => ({
        id: 1000 + i,
        title: `Legacy ${i}`,
        directory,
        pages: 1,
        legacy: true,
        artists: [],
        language: "japanese",
        type: "manga",
        formats: [],
        fallbacks: 0,
        missing: 0,
      }));
      await route.fulfill({
        json: {
          entries,
          total: 207,
          legacyTotal: remaining,
          page: 1,
          pageSize: 20,
          scan: {
            root,
            state: scanning ? "running" : "completed",
            visited: 208,
            found: 207,
            current: root,
            warnings: [],
            warningCount: 0,
          },
        },
      });
      return;
    }
    if (path === "/api/v1/library/migration-targets") {
      snapshots++;
      await route.fulfill({ json: { directories, skipped: 2 } });
      return;
    }
    if (path === "/api/v1/library/migrate") {
      const dirs = route.request().postDataJSON().directories as string[];
      batches.push(dirs);
      remaining -= dirs.length;
      await route.fulfill({ json: { accepted: dirs, errors: {} } });
      return;
    }
    if (path === "/api/v1/library/cover" || path === "/favicon.ico") {
      await route.fulfill({ status: 404 });
      return;
    }
    const asset = resolve("dist", path === "/" ? "index.html" : path.slice(1));
    await route.fulfill({
      contentType: asset.endsWith(".js")
        ? "application/javascript"
        : asset.endsWith(".css")
          ? "text/css"
          : "text/html",
      body: await readFile(asset),
    });
  });
  await page.goto("http://hitomi.test/");
  await page.getByRole("button", { name: "已下载", exact: true }).click();
  const button = page.getByRole("button", { name: "全部迁移", exact: true });
  await expect(button).toBeDisabled();
  scanning = false;
  await expect(button).toBeEnabled();
  expect(batches).toHaveLength(0);
  await button.click();
  await expect(page.getByText("迁移已加入队列", { exact: true })).toBeVisible();
  expect(snapshots).toBe(1);
  expect(batches.map((v) => v.length)).toEqual([100, 100, 5]);
  expect(batches.flat()).toEqual(directories);
  expect(new Set(batches.flat()).size).toBe(205);
  await expect(page.getByText(/跳过 2 个已有活动任务/)).toBeVisible();
  expect(errors).toEqual([]);
});

test("队列支持全部暂停、全部开始、隐藏和移除已完成任务", async ({ page }) => {
  let tasks = [
    {
      id: 1,
      title: "运行中的示例",
      state: "running",
      operation: "download",
      directory: "/downloads/one",
      done: 1,
      total: 2,
      errors: {},
    },
    {
      id: 2,
      title: "暂停中的示例",
      state: "paused",
      operation: "repair",
      directory: "/downloads/two",
      done: 0,
      total: 2,
      errors: {},
    },
    {
      id: 3,
      title: "已完成示例",
      state: "completed",
      operation: "migrate",
      directory: "/downloads/three",
      done: 2,
      total: 2,
      errors: {},
    },
  ];
  const actions: string[] = [];
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("http://hitomi.test/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/events") {
      await route.fulfill({
        contentType: "text/event-stream",
        body: `data: ${JSON.stringify(tasks)}\n\n`,
      });
      return;
    }
    if (path === "/api/v1/settings") {
      await route.fulfill({ json: { downloadDir: "/downloads" } });
      return;
    }
    if (path === "/api/v1/downloads") {
      await route.fulfill({ json: tasks });
      return;
    }
    if (path.startsWith("/api/v1/downloads/")) {
      const action = path.split("/").pop()!;
      actions.push(action);
      let affected = 0;
      if (action === "pause-all")
        tasks = tasks.map((t) => {
          if (["queued", "running"].includes(t.state)) {
            affected++;
            return { ...t, state: "paused" };
          }
          return t;
        });
      else if (action === "resume-all")
        tasks = tasks.map((t) => {
          if (["paused", "failed", "cancelled"].includes(t.state)) {
            affected++;
            return { ...t, state: "queued" };
          }
          return t;
        });
      else if (action === "clear-completed") {
        const remaining = tasks.filter((t) => t.state !== "completed");
        affected = tasks.length - remaining.length;
        tasks = remaining;
      } else throw new Error(`unexpected task action ${action}`);
      await route.fulfill({ json: { affected, skipped: 0, errors: {} } });
      return;
    }
    if (path === "/favicon.ico") {
      await route.fulfill({ status: 404 });
      return;
    }
    const asset = resolve("dist", path === "/" ? "index.html" : path.slice(1));
    await route.fulfill({
      contentType: asset.endsWith(".js")
        ? "application/javascript"
        : asset.endsWith(".css")
          ? "text/css"
          : "text/html",
      body: await readFile(asset),
    });
  });
  await page.goto("http://hitomi.test/");
  await page.getByRole("button", { name: /^下载队列/ }).click();
  await expect(page.getByText("已完成示例", { exact: true })).toBeVisible();
  await page.getByRole("checkbox", { name: "隐藏已完成", exact: true }).check();
  await expect(page.getByText("已完成示例", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "全部暂停", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "全部暂停", exact: true }),
  ).toBeDisabled();
  expect(tasks.filter((t) => t.state === "paused")).toHaveLength(2);
  await page.getByRole("button", { name: "全部开始", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "全部开始", exact: true }),
  ).toBeDisabled();
  expect(tasks.filter((t) => t.state === "queued")).toHaveLength(2);
  await page
    .getByRole("button", { name: "移除已完成任务（1）", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "移除已完成任务（0）", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("checkbox", { name: "隐藏已完成", exact: true })
    .uncheck();
  await expect(page.getByText("已完成示例", { exact: true })).toHaveCount(0);
  await expect(page.getByText("运行中的示例", { exact: true })).toBeVisible();
  await expect(page.getByText("暂停中的示例", { exact: true })).toBeVisible();
  expect(actions).toEqual(["pause-all", "resume-all", "clear-completed"]);
  expect(errors).toEqual([]);
});
