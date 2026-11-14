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
  IconFileImport,
  IconInfoCircle,
  IconRefresh,
  IconSearch,
  IconShieldCheck,
  IconX,
} from "@tabler/icons-react";
import { api, Entry, LibraryPage, Task } from "./api";
import { Cover, Empty, errorText, report, titles } from "./common";

type Batch = { accepted: string[]; errors: Record<string, string> };
const PAGE_SIZE = 50;
const MIGRATE_HINT =
  "旧格式不会自动改写。迁移会备份原 metadata，读取现有图片并建立校验记录；缺失页面可在迁移后补全。";

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
  const [tagFilters, setTagFilters] = useState<string[]>([]);
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Map<string, Entry>>(new Map());
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [queueing, setQueueing] = useState(false);
  const [queueProgress, setQueueProgress] = useState("");
  const [cancelling, setCancelling] = useState(false);
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
  }, [directory, page, q, kind, tagFilters, revision, taskRevision, disabled]);

  const entries = data?.entries || [];
  const scanning = data?.scan.state === "running";
  const selectedEntries = Array.from(selected.values());
  const legacySelected = selectedEntries.filter((e) => e.legacy);
  const nativeSelected = selectedEntries.filter((e) => !e.legacy);
  const taskOf = (entry: Entry) =>
    tasks.find(
      (t) =>
        t.directory === entry.directory &&
        ["queued", "running"].includes(t.state),
    );
  const busy = (entry: Entry) => !!taskOf(entry);
  const filtered = !!query || tagFilters.length > 0 || kind !== "all";

  async function batch(
    op: "migrate" | "repair",
    values: Pick<Entry, "directory">[],
    skipped = 0,
  ) {
    if (!values.length) return;
    setQueueing(true);
    try {
      const result: Batch = { accepted: [], errors: {} };
      for (let offset = 0; offset < values.length; offset += 100) {
        setQueueProgress(`正在加入队列：${offset} / ${values.length}`);
        const part = await api<Batch>(
          op === "migrate" ? "/library/migrate" : "/library/repair/batch",
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
                ? `\n另有 ${failures.length - 5} 项未能加入，请稍后重试。`
                : ""),
          ),
        );
      if (result.accepted.length) {
        notifications.show({
          color: "teal",
          title: op === "migrate" ? "迁移已加入队列" : "验证已加入队列",
          message: `${result.accepted.length} 个作品${skipped ? `，跳过 ${skipped} 个已有活动任务的作品` : ""}${op === "migrate" ? "，仅处理现有文件，不重新下载" : ""}`,
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
        if (op === "repair") onQueued();
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
    setQueueProgress("正在准备全部迁移列表…");
    try {
      const targets = await api<{ directories: string[]; skipped: number }>(
        "/library/migration-targets",
      );
      if (!targets.directories.length) {
        notifications.show({
          title: "没有新的迁移任务",
          message: targets.skipped
            ? `${targets.skipped} 个作品已有活动任务，已跳过。`
            : "旧 metadata 已全部迁移。",
        });
        setRevision((v) => v + 1);
        return;
      }
      await batch(
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
    setQueueing(true);
    let accepted = 0;
    let failed = 0;
    try {
      let total = Infinity;
      const targets = new Set<string>();
      for (let index = 1; (index - 1) * 100 < total; index++) {
        setQueueProgress(`正在准备验证列表：${targets.size} 个作品`);
        const result = await api<LibraryPage>(
          `/library?kind=native&pageSize=100&page=${index}`,
        );
        total = result.total;
        if (!result.entries.length) break;
        for (const entry of result.entries) targets.add(entry.directory);
      }
      const directories = Array.from(targets);
      for (let offset = 0; offset < directories.length; offset += 100) {
        setQueueProgress(`正在加入验证队列：${offset} / ${directories.length}`);
        const queued = await api<Batch>("/library/repair/batch", {
          directories: directories.slice(offset, offset + 100),
        });
        accepted += queued.accepted.length;
        failed += Object.keys(queued.errors).length;
      }
      notifications.show({
        color: failed ? "orange" : "teal",
        title: "验证队列已更新",
        message: `已加入 ${accepted} 个作品${failed ? `，${failed} 个未能加入（可能已有活动任务）` : ""}`,
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
            aria-label="搜索已下载"
            placeholder="搜索已下载：标题、作者、ID 或 tag，如 female:glasses -tag:digital（按 / 聚焦）"
            data-hotkey-search
            value={query}
            onChange={(e) => {
              setQuery(e.currentTarget.value);
              setPage(1);
            }}
            leftSection={<IconSearch size={14} />}
          />
          <Select
            aria-label="metadata 格式筛选"
            value={kind}
            onChange={(v) => {
              setKind(v);
              setPage(1);
            }}
            allowDeselect={false}
            data={[
              { value: "all", label: "全部作品" },
              { value: "legacy", label: "待迁移" },
              { value: "native", label: "已使用新 metadata" },
            ]}
            w={150}
          />
          <MultiSelect
            aria-label="标签筛选"
            placeholder={tagFilters.length ? undefined : "标签筛选（需同时满足）"}
            searchable
            clearable
            limit={50}
            hidePickedOptions
            nothingFoundMessage="没有匹配的标签"
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
            重新扫描
          </Button>
          <Button
            variant="light"
            leftSection={<IconFileImport size={14} />}
            title="迁移当前下载目录中的所有旧 metadata，不受筛选或分页限制；请先完成扫描"
            disabled={
              !data ||
              data.scan.state !== "completed" ||
              data.legacyTotal === 0 ||
              queueing
            }
            onClick={migrateAll}
          >
            全部迁移
          </Button>
          <Button
            variant="light"
            leftSection={<IconShieldCheck size={14} />}
            disabled={
              scanning ||
              !data ||
              data.scan.found - data.legacyTotal === 0 ||
              queueing
            }
            onClick={verifyAll}
          >
            验证全部
          </Button>
        </div>
        <div className="subbar">
          <span>
            {loading && !data ? (
              <>
                <Loader size={10} mr={6} />
                正在读取目录…
              </>
            ) : data ? (
              <>
                {scanning ? "已发现" : "共"} {data.scan.found.toLocaleString()}{" "}
                个作品
                {data.legacyTotal > 0 && (
                  <>
                    {" · "}
                    {data.legacyTotal.toLocaleString()} 个待迁移
                    <Tooltip label={MIGRATE_HINT} multiline w={260}>
                      <IconInfoCircle
                        size={13}
                        style={{ marginLeft: 4, verticalAlign: "-2px" }}
                      />
                    </Tooltip>
                  </>
                )}
                {data.total !== data.scan.found &&
                  ` · 筛选结果 ${data.total} 个`}
                {queueProgress && ` · ${queueProgress}`}
              </>
            ) : null}
          </span>
          <div className="actions">
            {(entries.length > 0 || selected.size > 0) && (
              <>
                <Checkbox
                  size="xs"
                  label={`选择本页${selected.size ? ` · 已选 ${selected.size} 项` : ""}`}
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
                    onClick={() => batch("migrate", legacySelected)}
                  >
                    迁移所选（{legacySelected.length}）
                  </Button>
                ) : (
                  entries.some((e) => e.legacy) && (
                    <Button
                      size="compact-xs"
                      variant="light"
                      disabled={queueing}
                      onClick={() =>
                        batch(
                          "migrate",
                          entries.filter((e) => e.legacy && !busy(e)),
                        )
                      }
                    >
                      迁移本页旧格式
                    </Button>
                  )
                )}
                {nativeSelected.length > 0 && (
                  <Button
                    size="compact-xs"
                    variant="default"
                    disabled={queueing}
                    onClick={() => batch("repair", nativeSelected)}
                  >
                    验证所选（{nativeSelected.length}）
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
                清除筛选
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
                {cancelling ? "正在取消扫描" : "后台扫描中"} · 已检查{" "}
                {data?.scan.visited} 个目录
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
              取消扫描
            </Button>
          </div>
        )}
      </div>
      {data?.scan.state === "cancelled" && (
        <Alert color="gray" p="xs" mb="xs">
          扫描已取消，已发现的结果仍可浏览。
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
          <summary>{data.scan.warningCount} 个目录或 metadata 无法读取</summary>
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
            entry.language || "未知语言",
            entry.type,
            `${entry.pages} 页`,
            entry.formats.join(" / ").toUpperCase(),
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
                aria-label={`选择作品 ${entry.id}`}
                checked={selected.has(entry.directory)}
                onChange={() => toggle(entry)}
              />
              <Cover
                src={`/api/v1/library/cover?directory=${encodeURIComponent(entry.directory)}&v=${encodeURIComponent(entry.updatedAt || "")}`}
                alt={`${main} 封面`}
              />
              <div className="downloaded-info">
                <div className="d-title">
                  {entry.legacy && (
                    <Badge size="xs" color="orange" variant="light">
                      待迁移
                    </Badge>
                  )}
                  {running && (
                    <Badge size="xs" variant="light">
                      {running.operation === "migrate" ? "迁移中" : "处理中"}
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
                    <span className="warn"> · {entry.missing} 页需要验证并补全</span>
                  )}
                  {entry.fallbacks > 0 && (
                    <span className="warn"> · {entry.fallbacks} 页使用衍生格式</span>
                  )}
                </div>
                <EntryTags tags={entry.tags || []} onSelect={addTagFilter} />
              </div>
              <Tooltip label={entry.legacy ? "迁移" : "验证并补全"}>
                <ActionIcon
                  variant="light"
                  aria-label={entry.legacy ? "迁移" : "验证并补全"}
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
            </Paper>
          );
        })}
      </div>
      {!loading && !scanning && !entries.length && (
        <Empty
          text={
            filtered
              ? "没有匹配的已下载作品。"
              : "没有发现已下载作品。可在设置中选择已有下载目录。"
          }
        />
      )}
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
            aria-label={`按标签 ${label} 筛选`}
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
          {expanded ? "收起" : `+${tags.length - TAG_LIMIT}`}
        </Button>
      )}
    </div>
  );
}
