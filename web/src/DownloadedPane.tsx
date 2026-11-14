import { Fragment, useEffect, useState } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Loader,
  MultiSelect,
  Pagination,
  Paper,
  Select,
  Text,
  TextInput,
  Tooltip,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import {
  IconCloudDownload,
  IconFileImport,
  IconInfoCircle,
  IconRefresh,
  IconSearch,
  IconShieldCheck,
  IconX,
} from "@tabler/icons-react";
import { api, Entry, Gallery, LibraryPage, Task } from "./api";
import { Cover, Empty, errorText, report, titles } from "./common";
import { useConfirm } from "./confirm";
import { t as tr } from "./i18n";
import { Opened, WorkDrawer } from "./WorkDrawer";

type Batch = { accepted: string[]; errors: Record<string, string> };
const PAGE_SIZE = 50;
const coverUrl = (entry: Entry) =>
  `/api/v1/library/cover?directory=${encodeURIComponent(entry.directory)}&v=${encodeURIComponent(entry.updatedAt || "")}`;

// ローカルの metadata から詳細表示用の作品情報を組み立てる。関連作品だけはオンラインで取得する。
function toOpened(entry: Entry): Opened {
  const gallery: Gallery = {
    id: entry.id,
    title: entry.title,
    japanese_title: entry.japaneseTitle,
    language: entry.language,
    type: entry.type,
    files: new Array(entry.pages).fill({ name: "", width: 0, height: 0 }),
    artists: entry.artists.map((artist) => ({ artist })),
    tags: (entry.tags || []).map((t) => ({
      tag: t.name,
      female: t.namespace === "female" ? 1 : 0,
      male: t.namespace === "male" ? 1 : 0,
    })),
  };
  return { gallery, coverSrc: coverUrl(entry), directory: entry.directory };
}
const MIGRATE_HINT = tr(
  "旧格式不会自动改写。迁移会备份原 metadata，读取现有图片并建立校验记录；缺失页面可在迁移后补全。",
);

// 一覧用の短い日付（ローカル時刻）。
const formatDay = (iso: string) => {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};

export function DownloadedPane({
  directory,
  tasks,
  disabled,
  onQueued,
}: {
  directory: string;
  tasks: Task[];
  disabled: boolean;
  onQueued: () => void;
}) {
  const [data, setData] = useState<LibraryPage>();
  const [query, setQuery] = useState("");
  const [q] = useDebouncedValue(query, 250);
  const [kind, setKind] = useState<string | null>("all");
  // 並び順：updated＝メタデータ更新が新しい順、created＝ダウンロード（ディレクトリ作成）が新しい／古い順。
  const [sort, setSort] = useState<string | null>("updated");
  const [tagFilters, setTagFilters] = useState<string[]>([]);
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Map<string, Entry>>(new Map());
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [queueing, setQueueing] = useState(false);
  const [queueProgress, setQueueProgress] = useState("");
  const [cancelling, setCancelling] = useState(false);
  const [opened, setOpened] = useState<Opened>();
  const { confirm, element: confirmElement } = useConfirm();
  const taskRevision = tasks
    .map((t) => `${t.id}:${t.operation}:${t.state}`)
    .join("|");
  useEffect(() => {
    setPage(1);
    setSelected(new Map());
    setData(undefined);
  }, [directory]);
  useEffect(() => {
    if (disabled) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let active = true;
    async function load() {
      try {
        const params = new URLSearchParams({
          page: String(page),
          pageSize: String(PAGE_SIZE),
          q,
          kind: kind || "all",
          sort: sort || "updated",
        });
        for (const tag of tagFilters) params.append("tag", tag);
        const result = await api<LibraryPage>(
          `/library?${params}`,
          undefined,
          undefined,
          controller.signal,
        );
        if (!active) return;
        const last = Math.max(1, Math.ceil(result.total / PAGE_SIZE));
        if (page > last) {
          setPage(last);
          return;
        }
        setData(result);
        setError("");
        setLoading(false);
        if (result.scan.state === "running") {
          timer = setTimeout(load, 800);
        } else {
          setCancelling(false);
        }
      } catch (e) {
        if (active && !controller.signal.aborted) {
          setError(errorText(e));
          setLoading(false);
        }
      }
    }
    void load();
    return () => {
      active = false;
      controller.abort();
      clearTimeout(timer);
    };
  }, [directory, page, q, kind, sort, tagFilters, revision, taskRevision, disabled]);

  const entries = data?.entries || [];
  const scanning = data?.scan.state === "running";
  const selectedEntries = Array.from(selected.values());
  const legacySelected = selectedEntries.filter((e) => e.legacy);
  const nativeSelected = selectedEntries.filter((e) => !e.legacy);
  const updatable = selectedEntries.filter((e) => e.upstream === "changed");
  const taskOf = (entry: Entry) =>
    tasks.find(
      (t) =>
        t.directory === entry.directory &&
        ["queued", "running"].includes(t.state),
    );
  const busy = (entry: Entry) => !!taskOf(entry);
  const filtered = !!query || tagFilters.length > 0 || kind !== "all";

  // 複数作品に対する操作は、件数と影響を示して確認してから実行する。
  const MIGRATE_NOTE = tr("只检查现有图片，不重新下载、不修改图片；原 metadata 会备份为 metadata.legacy.json。");
  const REPAIR_NOTE = tr("对比 Hitomi 信息并校验本地文件，只补全缺失或损坏的页面；上游有更新时只报告，不覆盖本地文件。");
  async function confirmBatch(
    op: "migrate" | "repair" | "update",
    values: Pick<Entry, "directory">[],
    skipped = 0,
  ) {
    if (!values.length) return;
    const n = values.length;
    const ok = await confirm(
      op === "migrate"
        ? { title: tr("迁移旧格式"), message: tr("迁移 {n} 个作品。{note}", { n, note: MIGRATE_NOTE }), confirmLabel: tr("迁移 {n} 个作品", { n }) }
        : op === "update"
          ? { title: tr("应用上游更新"), message: tr("为 {n} 个作品下载上游替换或新增的页面。被替换的旧页面会保留在各作品的 superseded 目录。", { n }), confirmLabel: tr("更新 {n} 个作品", { n }), color: "blue" }
          : { title: tr("验证并补全"), message: tr("验证 {n} 个作品。{note}每个作品会请求一次 Hitomi。", { n, note: REPAIR_NOTE }), confirmLabel: tr("验证 {n} 个作品", { n }) },
    );
    if (ok) await batch(op, values, skipped);
  }
  async function batch(
    op: "migrate" | "repair" | "update",
    values: Pick<Entry, "directory">[],
    skipped = 0,
  ) {
    if (!values.length) return;
    setQueueing(true);
    try {
      const result: Batch = { accepted: [], errors: {} };
      for (let offset = 0; offset < values.length; offset += 100) {
        setQueueProgress(tr("正在加入队列：{offset} / {total}", { offset, total: values.length }));
        const part = await api<Batch>(
          op === "migrate"
            ? "/library/migrate"
            : op === "update"
              ? "/library/update"
              : "/library/repair/batch",
          {
            directories: values
              .slice(offset, offset + 100)
              .map((e) => e.directory),
          },
        );
        result.accepted.push(...part.accepted);
        Object.assign(result.errors, part.errors);
      }
      const failures = Object.entries(result.errors);
      if (failures.length)
        report(
          new Error(
            failures
              .slice(0, 5)
              .map(([path, message]) => `${path}: ${message}`)
              .join("\n") +
              (failures.length > 5
                ? "\n" + tr("另有 {n} 项未能加入，请稍后重试。", { n: failures.length - 5 })
                : ""),
          ),
        );
      if (result.accepted.length) {
        notifications.show({
          color: "teal",
          title:
            op === "migrate"
              ? tr("迁移已加入队列")
              : op === "update"
                ? tr("上游更新已加入队列")
                : tr("验证已加入队列"),
          message: tr("{n} 个作品", { n: result.accepted.length }) + (skipped ? tr("，跳过 {n} 个已有活动任务的作品", { n: skipped }) : "") + (op === "migrate" ? tr("，仅处理现有文件，不重新下载") : ""),
        });
        setSelected(
          (prev) =>
            new Map(
              Array.from(prev).filter(
                ([key]) => !result.accepted.includes(key),
              ),
            ),
        );
        setRevision((v) => v + 1);
        if (op !== "migrate") onQueued();
      }
    } catch (e) {
      report(e);
    } finally {
      setQueueing(false);
      setQueueProgress("");
    }
  }
  async function migrateAll() {
    setQueueing(true);
    setQueueProgress(tr("正在准备全部迁移列表…"));
    try {
      const targets = await api<{ directories: string[]; skipped: number }>(
        "/library/migration-targets",
      );
      if (!targets.directories.length) {
        notifications.show({
          title: tr("没有新的迁移任务"),
          message: targets.skipped
            ? tr("{n} 个作品已有活动任务，已跳过。", { n: targets.skipped })
            : tr("旧 metadata 已全部迁移。"),
        });
        setRevision((v) => v + 1);
        return;
      }
      setQueueing(false);
      setQueueProgress("");
      await confirmBatch(
        "migrate",
        targets.directories.map((directory) => ({ directory })),
        targets.skipped,
      );
    } catch (e) {
      report(e);
    } finally {
      setQueueing(false);
      setQueueProgress("");
    }
  }
  async function verifyAll() {
    const count = data ? data.scan.found - data.legacyTotal : 0;
    const ok = await confirm({
      title: tr("验证全部"),
      message: tr("验证全部约 {n} 个新格式作品，不受筛选和分页限制。{note}每个作品会请求一次 Hitomi，数量多时需要较长时间。", { n: count.toLocaleString(), note: REPAIR_NOTE }),
      confirmLabel: tr("验证 {n} 个作品", { n: count.toLocaleString() }),
    });
    if (!ok) return;
    setQueueing(true);
    let accepted = 0;
    let failed = 0;
    try {
      let total = Infinity;
      const targets = new Set<string>();
      for (let index = 1; (index - 1) * 100 < total; index++) {
        setQueueProgress(tr("正在准备验证列表：{n} 个作品", { n: targets.size }));
        const result = await api<LibraryPage>(
          `/library?kind=native&pageSize=100&page=${index}`,
        );
        total = result.total;
        if (!result.entries.length) break;
        for (const entry of result.entries) targets.add(entry.directory);
      }
      const directories = Array.from(targets);
      for (let offset = 0; offset < directories.length; offset += 100) {
        setQueueProgress(tr("正在加入验证队列：{offset} / {total}", { offset, total: directories.length }));
        const queued = await api<Batch>("/library/repair/batch", {
          directories: directories.slice(offset, offset + 100),
        });
        accepted += queued.accepted.length;
        failed += Object.keys(queued.errors).length;
      }
      notifications.show({
        color: failed ? "orange" : "teal",
        title: tr("验证队列已更新"),
        message: tr("已加入 {n} 个作品", { n: accepted }) + (failed ? tr("，{n} 个未能加入（可能已有活动任务）", { n: failed }) : ""),
      });
      if (accepted) onQueued();
    } catch (e) {
      report(e);
    } finally {
      setQueueing(false);
      setQueueProgress("");
    }
  }
  async function scan() {
    try {
      await api("/library/scan", {});
      setPage(1);
      setSelected(new Map());
      setRevision((v) => v + 1);
    } catch (e) {
      report(e);
    }
  }
  async function cancelScan() {
    try {
      setCancelling(true);
      await api("/library/scan/cancel", {});
      setRevision((v) => v + 1);
    } catch (e) {
      setCancelling(false);
      report(e);
    }
  }
  const toggle = (entry: Entry) =>
    setSelected((prev) => {
      const next = new Map(prev);
      if (next.has(entry.directory)) next.delete(entry.directory);
      else next.set(entry.directory, entry);
      return next;
    });
  const pageSelected =
    entries.length > 0 && entries.every((e) => selected.has(e.directory));
  const tagOptions = (data?.tags || []).map((tag) => ({
    value: tag.value,
    label: `${tag.value} (${tag.count})`,
  }));
  for (const value of tagFilters) {
    if (!tagOptions.some((option) => option.value === value))
      tagOptions.push({ value, label: value });
  }
  const addTagFilter = (value: string) => {
    setTagFilters((previous) =>
      previous.includes(value) ? previous : [...previous, value],
    );
    setPage(1);
  };

  return (
    <>
      <div className="sticky-head">
        <div className="toolbar">
          <TextInput
            className="grow"
            aria-label={tr("搜索已下载")}
            placeholder={tr("搜索已下载：标题、作者、ID 或 tag，如 female:glasses -tag:digital（按 / 聚焦）")}
            data-hotkey-search
            value={query}
            onChange={(e) => {
              setQuery(e.currentTarget.value);
              setPage(1);
            }}
            leftSection={<IconSearch size={14} />}
          />
          <Select
            aria-label={tr("metadata 格式筛选")}
            value={kind}
            onChange={(v) => {
              setKind(v);
              setPage(1);
            }}
            allowDeselect={false}
            data={[
              { value: "all", label: tr("全部作品") },
              { value: "legacy", label: tr("待迁移") },
              { value: "native", label: tr("已使用新 metadata") },
              { value: "upstream", label: tr("上游有更新 / 已删除") },
            ]}
            w={150}
          />
          <Select
            aria-label={tr("排序方式")}
            value={sort}
            onChange={(v) => {
              setSort(v);
              setPage(1);
            }}
            allowDeselect={false}
            data={[
              { value: "updated", label: tr("最近更新") },
              { value: "created", label: tr("下载时间：新→旧") },
              { value: "created-asc", label: tr("下载时间：旧→新") },
            ]}
            w={150}
          />
          <MultiSelect
            aria-label={tr("标签筛选")}
            placeholder={tagFilters.length ? undefined : tr("标签筛选（需同时满足）")}
            searchable
            clearable
            limit={50}
            hidePickedOptions
            nothingFoundMessage={tr("没有匹配的标签")}
            data={tagOptions}
            value={tagFilters}
            onChange={(tags) => {
              setTagFilters(tags);
              setPage(1);
            }}
            w={260}
          />
          <Button
            variant="default"
            leftSection={<IconRefresh size={14} />}
            disabled={scanning || queueing}
            onClick={scan}
          >
            {tr("重新扫描")}
          </Button>
          <Button
            variant="light"
            leftSection={<IconFileImport size={14} />}
            title={tr("迁移当前下载目录中的所有旧 metadata，不受筛选或分页限制；请先完成扫描")}
            disabled={
              !data ||
              data.scan.state !== "completed" ||
              data.legacyTotal === 0 ||
              queueing
            }
            onClick={migrateAll}
          >
            {tr("全部迁移")}
          </Button>
          <Button
            variant="light"
            leftSection={<IconShieldCheck size={14} />}
            title={tr("对比 Hitomi 信息并校验本地文件（每个作品请求一次 Hitomi）；上游有更新时只报告，不覆盖本地文件")}
            disabled={
              scanning ||
              !data ||
              data.scan.found - data.legacyTotal === 0 ||
              queueing
            }
            onClick={verifyAll}
          >
            {tr("验证全部")}
          </Button>
        </div>
        <div className="subbar">
          <span>
            {loading && !data ? (
              <>
                <Loader size={10} mr={6} />
                {tr("正在读取目录…")}
              </>
            ) : data ? (
              <>
                {scanning
                  ? tr("已发现 {n} 个作品", { n: data.scan.found.toLocaleString() })
                  : tr("共 {n} 个作品", { n: data.scan.found.toLocaleString() })}
                {data.legacyTotal > 0 && (
                  <>
                    {" · "}
                    {tr("{n} 个待迁移", { n: data.legacyTotal.toLocaleString() })}
                    <Tooltip label={MIGRATE_HINT} multiline w={260}>
                      <IconInfoCircle
                        size={13}
                        style={{ marginLeft: 4, verticalAlign: "-2px" }}
                      />
                    </Tooltip>
                  </>
                )}
                {data.total !== data.scan.found &&
                  ` · ${tr("筛选结果 {n} 个", { n: data.total })}`}
                {queueProgress && ` · ${queueProgress}`}
              </>
            ) : null}
          </span>
          <div className="actions">
            {(entries.length > 0 || selected.size > 0) && (
              <>
                <Checkbox
                  size="xs"
                  label={tr("选择本页") + (selected.size ? ` · ${tr("已选 {n} 项", { n: selected.size })}` : "")}
                  checked={pageSelected}
                  indeterminate={
                    !pageSelected &&
                    entries.some((e) => selected.has(e.directory))
                  }
                  onChange={() =>
                    setSelected((prev) => {
                      const next = new Map(prev);
                      for (const e of entries) {
                        if (pageSelected) next.delete(e.directory);
                        else next.set(e.directory, e);
                      }
                      return next;
                    })
                  }
                />
                {legacySelected.length > 0 ? (
                  <Button
                    size="compact-xs"
                    variant="light"
                    disabled={queueing}
                    onClick={() => confirmBatch("migrate", legacySelected)}
                  >
                    {tr("迁移所选（{n}）", { n: legacySelected.length })}
                  </Button>
                ) : (
                  entries.some((e) => e.legacy) && (
                    <Button
                      size="compact-xs"
                      variant="light"
                      disabled={queueing}
                      onClick={() =>
                        confirmBatch(
                          "migrate",
                          entries.filter((e) => e.legacy && !busy(e)),
                        )
                      }
                    >
                      {tr("迁移本页旧格式")}
                    </Button>
                  )
                )}
                {nativeSelected.length > 0 && (
                  <Button
                    size="compact-xs"
                    variant="default"
                    disabled={queueing}
                    onClick={() => confirmBatch("repair", nativeSelected)}
                  >
                    {tr("验证所选（{n}）", { n: nativeSelected.length })}
                  </Button>
                )}
                {updatable.length > 0 && (
                  <Button
                    size="compact-xs"
                    variant="light"
                    color="blue"
                    title={tr("下载上游替换或新增的页面；被替换的旧页面保留在 superseded 目录")}
                    disabled={queueing}
                    onClick={() => confirmBatch("update", updatable)}
                  >
                    {tr("应用上游更新（{n}）", { n: updatable.length })}
                  </Button>
                )}
              </>
            )}
            {filtered && (
              <Button
                size="compact-xs"
                variant="subtle"
                color="gray"
                onClick={() => {
                  setQuery("");
                  setTagFilters([]);
                  setKind("all");
                  setPage(1);
                }}
              >
                {tr("清除筛选")}
              </Button>
            )}
          </div>
        </div>
        {scanning && (
          <div className="subbar" style={{ flexWrap: "nowrap" }}>
            <span
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                minWidth: 0,
              }}
            >
              <Loader size={10} />
              <span style={{ flex: "none" }}>
                {cancelling ? tr("正在取消扫描") : tr("后台扫描中")} ·{" "}
                {tr("已检查 {n} 个目录", { n: data?.scan.visited ?? 0 })}
              </span>
              <Text
                span
                size="xs"
                c="dimmed"
                truncate
                title={data?.scan.current}
                style={{ minWidth: 0 }}
              >
                {data?.scan.current}
              </Text>
            </span>
            <Button
              size="compact-xs"
              variant="subtle"
              color="gray"
              leftSection={<IconX size={12} />}
              loading={cancelling}
              onClick={cancelScan}
            >
              {tr("取消扫描")}
            </Button>
          </div>
        )}
      </div>
      {data?.scan.state === "cancelled" && (
        <Alert color="gray" p="xs" mb="xs">
          {tr("扫描已取消，已发现的结果仍可浏览。")}
        </Alert>
      )}
      {error && (
        <Alert color="red" p="xs" mb="xs">
          {error}
        </Alert>
      )}
      {data?.scan.error && (
        <Alert color="red" p="xs" mb="xs">
          {data.scan.error}
        </Alert>
      )}
      {data && data.scan.warningCount > 0 && (
        <details className="scan-warnings">
          <summary>{tr("{n} 个目录或 metadata 无法读取", { n: data.scan.warningCount })}</summary>
          {data.scan.warnings.map((w, i) => (
            <Text key={i} size="xs" className="path" mt={4}>
              {w}
            </Text>
          ))}
        </details>
      )}
      <div className="rows">
        {entries.map((entry) => {
          const { main, sub } = titles(entry.title, entry.japaneseTitle);
          const running = taskOf(entry);
          const meta = [
            `#${entry.id}`,
            entry.artists.length ? entry.artists.join("、") : "",
            entry.language || tr("未知语言"),
            entry.type,
            tr("{n} 页", { n: entry.pages }),
            entry.formats.join(" / ").toUpperCase(),
            entry.createdAt ? tr("下载于 {time}", { time: formatDay(entry.createdAt) }) : "",
          ].filter(Boolean);
          return (
            <Paper
              withBorder
              key={entry.directory}
              className="downloaded-row"
              title={entry.directory}
            >
              <Checkbox
                size="xs"
                aria-label={tr("选择作品 {id}", { id: entry.id })}
                checked={selected.has(entry.directory)}
                onChange={() => toggle(entry)}
              />
              <Cover
                src={coverUrl(entry)}
                alt={tr("{title} 封面", { title: main })}
                onClick={() => setOpened(toOpened(entry))}
              />
              <div className="downloaded-info">
                <div
                  className="d-title"
                  onClick={() => setOpened(toOpened(entry))}
                >
                  {entry.legacy && (
                    <Badge size="xs" color="orange" variant="light">
                      {tr("待迁移")}
                    </Badge>
                  )}
                  {entry.upstream === "changed" && (
                    <Badge size="xs" color="blue" variant="light">
                      {tr("上游有更新")}
                    </Badge>
                  )}
                  {entry.upstream === "deleted" && (
                    <Badge size="xs" color="gray" variant="light">
                      {tr("上游已删除")}
                    </Badge>
                  )}
                  {running && (
                    <Badge size="xs" variant="light">
                      {running.operation === "migrate" ? tr("迁移中") : tr("处理中")}
                    </Badge>
                  )}
                  <span className="main-title" title={main}>
                    {main}
                  </span>
                  {sub && (
                    <span className="work-title-secondary" title={sub}>
                      {sub}
                    </span>
                  )}
                </div>
                <div className="d-meta">
                  {meta.map((part, i) => (
                    <Fragment key={i}>
                      {i > 0 && " · "}
                      <span>{part}</span>
                    </Fragment>
                  ))}
                  {entry.missing > 0 && (
                    <span className="warn"> · {tr("{n} 页需要验证并补全", { n: entry.missing })}</span>
                  )}
                  {entry.upstream === "changed" && entry.upstreamNote && (
                    <span className="info"> · {tr("上游：{note}", { note: entry.upstreamNote })}</span>
                  )}
                  {entry.fallbacks > 0 && (
                    <span className="warn"> · {tr("{n} 页使用衍生格式", { n: entry.fallbacks })}</span>
                  )}
                </div>
                <EntryTags tags={entry.tags || []} onSelect={addTagFilter} />
              </div>
              <div className="d-actions">
                {entry.upstream === "changed" && (
                  <Tooltip label={tr("应用上游更新（旧页面保留在 superseded）")}>
                    <ActionIcon
                      variant="light"
                      color="blue"
                      aria-label={tr("应用上游更新")}
                      disabled={busy(entry) || queueing}
                      onClick={() => batch("update", [entry])}
                    >
                      <IconCloudDownload size={16} />
                    </ActionIcon>
                  </Tooltip>
                )}
                <Tooltip label={entry.legacy ? tr("迁移") : tr("验证并补全")}>
                  <ActionIcon
                    variant="light"
                    aria-label={entry.legacy ? tr("迁移") : tr("验证并补全")}
                    disabled={busy(entry) || queueing}
                    onClick={() =>
                      batch(entry.legacy ? "migrate" : "repair", [entry])
                    }
                  >
                    {entry.legacy ? (
                      <IconFileImport size={16} />
                    ) : (
                      <IconShieldCheck size={16} />
                    )}
                  </ActionIcon>
                </Tooltip>
              </div>
            </Paper>
          );
        })}
      </div>
      {!loading && !scanning && !entries.length && (
        <Empty
          text={
            filtered
              ? tr("没有匹配的已下载作品。")
              : tr("没有发现已下载作品。可在设置中选择已有下载目录。")
          }
        />
      )}
      {confirmElement}
      <WorkDrawer opened={opened} onClose={() => setOpened(undefined)} />
      {data && data.total > PAGE_SIZE && (
        <div className="pager">
          <Pagination
            total={Math.ceil(data.total / PAGE_SIZE)}
            value={page}
            onChange={setPage}
            size="sm"
          />
        </div>
      )}
    </>
  );
}

const TAG_LIMIT = 6;

function EntryTags({
  tags,
  onSelect,
}: {
  tags: Entry["tags"];
  onSelect: (value: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  if (!tags.length) return null;
  return (
    <div className="downloaded-tags">
      {(expanded ? tags : tags.slice(0, TAG_LIMIT)).map((tag) => {
        const label = `${tag.namespace}:${tag.name}`;
        const value = label
          .toLowerCase()
          .replaceAll("_", " ")
          .replace(/\s+/g, " ")
          .trim();
        return (
          <Button
            key={value}
            size="compact-xs"
            variant="light"
            color={
              tag.namespace === "female"
                ? "pink"
                : tag.namespace === "male"
                  ? "blue"
                  : "gray"
            }
            title={label}
            aria-label={tr("按标签 {label} 筛选", { label })}
            onClick={() => onSelect(value)}
            style={{ maxWidth: "100%", height: 18, paddingInline: 6 }}
          >
            <Text component="span" fz={11} truncate>
              {label}
            </Text>
          </Button>
        );
      })}
      {tags.length > TAG_LIMIT && (
        <Button
          size="compact-xs"
          variant="subtle"
          color="gray"
          style={{ height: 18, paddingInline: 6 }}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? tr("收起") : `+${tags.length - TAG_LIMIT}`}
        </Button>
      )}
    </div>
  );
}
