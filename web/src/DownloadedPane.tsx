import { useEffect, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Image,
  Loader,
  MultiSelect,
  Pagination,
  Paper,
  Select,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import {
  IconFileImport,
  IconPhotoOff,
  IconRefresh,
  IconSearch,
  IconShieldCheck,
  IconX,
} from "@tabler/icons-react";
import { api, Entry, LibraryPage, Task } from "./api";

type Batch = { accepted: string[]; errors: Record<string, string> };
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
          pageSize: "20",
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
        const last = Math.max(1, Math.ceil(result.total / 20));
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
          setError(e instanceof Error ? e.message : String(e));
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
  const report = (e: unknown) =>
    notifications.show({
      color: "red",
      title: "操作未完成",
      message: e instanceof Error ? e.message : String(e),
      autoClose: false,
    });
  const entries = data?.entries || [];
  const scanning = data?.scan.state === "running";
  const selectedEntries = Array.from(selected.values());
  const legacySelected = selectedEntries.filter((e) => e.legacy);
  const nativeSelected = selectedEntries.filter((e) => !e.legacy);
  const busy = (entry: Entry) =>
    tasks.some(
      (t) =>
        t.directory === entry.directory &&
        ["queued", "running"].includes(t.state),
    );
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
  return (
    <Stack gap="md">
      <Paper withBorder p="md">
        <Stack gap="sm">
          <Group wrap="wrap" align="flex-end">
            <TextInput
              label="搜索已下载"
              placeholder="标题、作者、ID 或 tag，如 female:glasses"
              value={query}
              onChange={(e) => {
                setQuery(e.currentTarget.value);
                setPage(1);
              }}
              leftSection={<IconSearch size={17} />}
              style={{ flex: 1, minWidth: 180 }}
            />
            <Select
              aria-label="metadata 格式筛选"
              value={kind}
              onChange={(v) => {
                setKind(v);
                setPage(1);
              }}
              data={[
                { value: "all", label: "全部作品" },
                { value: "legacy", label: "待迁移" },
                { value: "native", label: "已使用新 metadata" },
              ]}
              w={180}
            />
            <Button
              variant="default"
              leftSection={<IconRefresh size={16} />}
              disabled={scanning || queueing}
              onClick={scan}
            >
              重新扫描
            </Button>
            <Button
              variant="light"
              leftSection={<IconFileImport size={16} />}
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
              leftSection={<IconShieldCheck size={16} />}
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
          </Group>
          <Group align="flex-end">
            <MultiSelect
              label="标签筛选"
              placeholder="搜索并选择标签（多个标签需同时满足）"
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
              style={{ flex: 1, minWidth: 220 }}
            />
            <Button
              variant="subtle"
              color="gray"
              disabled={!query && !tagFilters.length && kind === "all"}
              onClick={() => {
                setQuery("");
                setTagFilters([]);
                setKind("all");
                setPage(1);
              }}
            >
              清除筛选
            </Button>
          </Group>
          <Text size="xs" c="dimmed">
            输入后自动搜索本地记录，空格分隔多个条件；支持 -tag:digital
            等排除条件。点击列表中的标签可加入筛选。
          </Text>
          <Text size="xs" c="dimmed" className="path">
            {directory}
          </Text>
        </Stack>
      </Paper>
      {loading && !data && (
        <Group>
          <Loader size="xs" />
          <Text size="sm" c="dimmed">
            正在读取目录…
          </Text>
        </Group>
      )}
      {data && (
        <Group justify="space-between">
          <Text size="sm" c="dimmed">
            {scanning ? "已发现" : "共"} {data.scan.found.toLocaleString()}{" "}
            个作品 · {data.legacyTotal.toLocaleString()} 个待迁移
            {data.total !== data.scan.found && ` · 筛选结果 ${data.total} 个`}
          </Text>
          {scanning && (
            <Button
              variant="subtle"
              color="gray"
              size="xs"
              leftSection={<IconX size={14} />}
              loading={cancelling}
              onClick={async () => {
                try {
                  setCancelling(true);
                  await api("/library/scan/cancel", {});
                  setRevision((v) => v + 1);
                } catch (e) {
                  setCancelling(false);
                  report(e);
                }
              }}
            >
              取消扫描
            </Button>
          )}
        </Group>
      )}
      {scanning && (
        <Paper p="sm" withBorder>
          <Group gap="sm" wrap="nowrap">
            <Loader size="xs" />
            <div style={{ minWidth: 0 }}>
              <Text size="sm">
                {cancelling
                  ? "正在取消扫描"
                  : "后台扫描中，可以先浏览已发现的作品"}{" "}
                · 已检查 {data?.scan.visited} 个目录
              </Text>
              <Text size="xs" c="dimmed" truncate title={data?.scan.current}>
                {data?.scan.current}
              </Text>
            </div>
          </Group>
        </Paper>
      )}
      {data?.scan.state === "cancelled" && (
        <Alert color="gray">扫描已取消，已发现的结果仍可浏览。</Alert>
      )}
      {error && <Alert color="red">{error}</Alert>}
      {data?.scan.error && <Alert color="red">{data.scan.error}</Alert>}
      {data && data.scan.warningCount > 0 && (
        <details className="scan-warnings">
          <summary>{data.scan.warningCount} 个目录或 metadata 无法读取</summary>
          {data.scan.warnings.map((w, i) => (
            <Text key={i} size="xs" className="path" mt={5}>
              {w}
            </Text>
          ))}
        </details>
      )}
      {data && data.legacyTotal > 0 && (
        <Alert
          color="blue"
          icon={<IconFileImport size={18} />}
          title="发现旧 metadata"
        >
          旧格式不会自动改写。迁移会备份原
          metadata，读取现有图片并建立校验记录；缺失页面可在迁移后补全。
        </Alert>
      )}
      {(entries.length > 0 || selected.size > 0) && (
        <Group justify="space-between" className="downloaded-selection">
          <Checkbox
            label={`选择本页${selected.size ? ` · 已选 ${selected.size} 项` : ""}`}
            checked={pageSelected}
            indeterminate={
              !pageSelected && entries.some((e) => selected.has(e.directory))
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
          <Group gap="xs">
            {legacySelected.length > 0 ? (
              <Button
                size="xs"
                variant="light"
                leftSection={<IconFileImport size={14} />}
                disabled={queueing}
                onClick={() => batch("migrate", legacySelected)}
              >
                迁移所选（{legacySelected.length}）
              </Button>
            ) : (
              entries.some((e) => e.legacy) && (
                <Button
                  size="xs"
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
                size="xs"
                variant="default"
                disabled={queueing}
                onClick={() => batch("repair", nativeSelected)}
              >
                验证所选（{nativeSelected.length}）
              </Button>
            )}
          </Group>
        </Group>
      )}
      {queueProgress && (
        <Text size="sm" c="dimmed">
          {queueProgress}
        </Text>
      )}
      <Stack gap="sm">
        {entries.map((entry) => (
          <Paper
            withBorder
            p="md"
            key={entry.directory}
            className="downloaded-row"
          >
            <Checkbox
              aria-label={`选择作品 ${entry.id}`}
              checked={selected.has(entry.directory)}
              onChange={() =>
                setSelected((prev) => {
                  const next = new Map(prev);
                  if (next.has(entry.directory)) next.delete(entry.directory);
                  else next.set(entry.directory, entry);
                  return next;
                })
              }
            />
            <Image
              loading="lazy"
              src={`/api/v1/library/cover?directory=${encodeURIComponent(entry.directory)}&v=${encodeURIComponent(entry.updatedAt || "")}`}
              w={88}
              h={124}
              fit="contain"
              radius="sm"
              className="cover"
              alt={`${entry.japaneseTitle?.trim() || entry.title} 封面`}
              fallbackSrc="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='88' height='124'%3E%3Crect fill='%232a2e35' width='88' height='124'/%3E%3Ctext x='44' y='65' fill='%23868e96' font-size='12' text-anchor='middle'%3E%E6%9A%82%E6%97%A0%E5%B0%81%E9%9D%A2%3C/text%3E%3C/svg%3E"
            />
            <div className="downloaded-info">
              <Group gap={6} mb={6}>
                <Badge size="xs" variant="outline" color="gray">
                  #{entry.id}
                </Badge>
                {entry.legacy ? (
                  <Badge size="xs" color="orange" variant="light">
                    待迁移
                  </Badge>
                ) : (
                  <Badge size="xs" color="teal" variant="light">
                    已下载
                  </Badge>
                )}
                {busy(entry) && (
                  <Badge size="xs" variant="light">
                    {tasks.find((t) => t.directory === entry.directory)
                      ?.operation === "migrate"
                      ? "迁移中"
                      : "处理中"}
                  </Badge>
                )}
              </Group>
              <Title
                order={3}
                size="md"
                lineClamp={2}
                title={entry.japaneseTitle?.trim() || entry.title}
              >
                {entry.japaneseTitle?.trim() || entry.title}
              </Title>
              {entry.japaneseTitle?.trim() &&
                entry.title.trim() &&
                entry.japaneseTitle.trim() !== entry.title.trim() && (
                  <Text
                    size="xs"
                    c="dimmed"
                    lineClamp={1}
                    mt={3}
                    className="work-title-secondary"
                  >
                    {entry.title}
                  </Text>
                )}
              <Group gap="sm" mt={7}>
                <Text size="sm" fw={600}>
                  {entry.pages} 页
                </Text>
                <Text size="xs" c="dimmed">
                  {entry.language || "未知语言"}
                  {entry.type && ` · ${entry.type}`}
                  {entry.formats.length > 0 &&
                    ` · ${entry.formats.join(" / ").toUpperCase()}`}
                </Text>
              </Group>
              {entry.artists.length > 0 && (
                <Text size="xs" c="dimmed" mt={4} lineClamp={1}>
                  作者：{entry.artists.join("、")}
                </Text>
              )}
              <EntryTags
                tags={entry.tags || []}
                onSelect={(value) => {
                  setTagFilters((previous) =>
                    previous.includes(value) ? previous : [...previous, value],
                  );
                  setPage(1);
                }}
              />
              {entry.missing > 0 && (
                <Text size="xs" c="orange" mt={4}>
                  {entry.missing} 页需要验证并补全
                </Text>
              )}
              {entry.fallbacks > 0 && (
                <Text size="xs" c="orange" mt={4}>
                  {entry.fallbacks} 页使用衍生格式
                </Text>
              )}
              <Text
                size="xs"
                c="dimmed"
                mt={5}
                truncate
                title={entry.directory}
              >
                {entry.directory}
              </Text>
            </div>
            <Stack gap="xs" className="downloaded-actions">
              {entry.legacy ? (
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconFileImport size={15} />}
                  disabled={busy(entry) || queueing}
                  onClick={() => batch("migrate", [entry])}
                >
                  迁移
                </Button>
              ) : (
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconShieldCheck size={15} />}
                  disabled={busy(entry) || queueing}
                  onClick={() => batch("repair", [entry])}
                >
                  验证并补全
                </Button>
              )}
            </Stack>
          </Paper>
        ))}
      </Stack>
      {!loading && !scanning && !entries.length && (
        <Paper p="xl" withBorder ta="center">
          <IconPhotoOff size={28} color="var(--mantine-color-dimmed)" />
          <Text c="dimmed" size="sm" mt="sm">
            {q || tagFilters.length > 0 || kind !== "all"
              ? "没有匹配的已下载作品。"
              : "没有发现已下载作品。可在设置中选择已有下载目录。"}
          </Text>
        </Paper>
      )}
      {data && data.total > 20 && (
        <Pagination
          total={Math.ceil(data.total / 20)}
          value={page}
          onChange={setPage}
          mx="auto"
        />
      )}
    </Stack>
  );
}

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
    <Group gap={5} mt={7} className="downloaded-tags">
      {(expanded ? tags : tags.slice(0, 8)).map((tag) => {
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
            style={{ maxWidth: "100%" }}
          >
            <Text component="span" size="xs" truncate>
              {label}
            </Text>
          </Button>
        );
      })}
      {tags.length > 8 && (
        <Button
          size="compact-xs"
          variant="subtle"
          color="gray"
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? "收起标签" : `展开 ${tags.length - 8} 个标签`}
        </Button>
      )}
    </Group>
  );
}
