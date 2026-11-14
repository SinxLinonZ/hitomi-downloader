import { useEffect, useMemo, useRef, useState } from "react";
import {
  ActionIcon,
  Button,
  Drawer,
  Group,
  Indicator,
  Loader,
  NativeSelect,
  Text,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconAdjustmentsHorizontal, IconSearch, IconX } from "@tabler/icons-react";
import { api } from "../api";
import { loadProgress, loadSort, saveSort, SORT_OPTIONS, SortKey } from "./progress";
import type { ReaderEntry, ReaderLibrary } from "./types";

const PAGE_SIZE = 30;
const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));
const titleOf = (e: ReaderEntry) => e.japaneseTitle?.trim() || e.title;
const tagValue = (t: { name: string; namespace: string }) =>
  `${t.namespace}:${t.name}`.toLowerCase().replaceAll("_", " ");

export function ReaderList({
  onOpen,
  progressRevision,
  filterOpen,
  onFilterOpen,
  onFilterClose,
}: {
  onOpen: (id: number) => void;
  progressRevision: number;
  filterOpen: boolean;
  onFilterOpen: () => void;
  onFilterClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [q] = useDebouncedValue(query, 300);
  const [selected, setSelected] = useState<string[]>([]);
  const [page, setPage] = useState(1);
  const [revision, setRevision] = useState(0);
  const [entries, setEntries] = useState<ReaderEntry[]>([]);
  const [meta, setMeta] = useState<Pick<ReaderLibrary, "total" | "found" | "scanning" | "tags">>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [sort, setSort] = useState<SortKey>(loadSort);
  const [tagQuery, setTagQuery] = useState("");
  const sentinel = useRef<HTMLDivElement>(null);
  const progress = useMemo(() => loadProgress(), [progressRevision, entries]);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    setLoading(true);
    const params = new URLSearchParams({ page: String(page), pageSize: String(PAGE_SIZE), q, sort });
    for (const tag of selected) params.append("tag", tag);
    api<ReaderLibrary>(`/reader/library?${params}`, undefined, undefined, controller.signal)
      .then((r) => {
        setEntries((prev) => {
          if (page === 1) return r.entries;
          const seen = new Set(prev.map((e) => e.id));
          return [...prev, ...r.entries.filter((e) => !seen.has(e.id))];
        });
        setMeta({ total: r.total, found: r.found, scanning: r.scanning, tags: r.tags });
        setError("");
        setLoading(false);
        // 起動直後の走査中は、見つかった作品が増えるのを定期的に反映する。
        if (r.scanning) timer = setTimeout(() => setRevision((v) => v + 1), 1500);
      })
      .catch((e) => {
        if (!controller.signal.aborted) {
          setError(errorText(e));
          setLoading(false);
        }
      });
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [q, selected, sort, page, revision]);

  const hasMore = !!meta && entries.length < meta.total;
  // 末尾が見えたら次のページを読み込む。
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !hasMore || loading) return;
    const observer = new IntersectionObserver(
      (items) => {
        if (items.some((i) => i.isIntersecting)) setPage((p) => p + 1);
      },
      { rootMargin: "600px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasMore, loading, entries.length]);

  const tagOptions = useMemo(() => {
    const needle = tagQuery.trim().toLowerCase().replaceAll("_", " ");
    return (meta?.tags || [])
      .filter((t) => !selected.includes(t.value) && (!needle || t.value.includes(needle)))
      .slice(0, 80);
  }, [meta?.tags, tagQuery, selected]);
  const resetTo = (next: string[]) => {
    setSelected(next);
    setPage(1);
  };

  return (
    <div className="r-list">
      <div className="r-top">
        <TextInput
          style={{ flex: 1 }}
          aria-label="搜索作品"
          placeholder="搜索标题、作者、标签"
          leftSection={<IconSearch size={18} />}
          rightSection={
            query ? (
              <ActionIcon variant="subtle" color="gray" aria-label="清除搜索" onClick={() => { setQuery(""); setPage(1); }}>
                <IconX size={16} />
              </ActionIcon>
            ) : undefined
          }
          value={query}
          onChange={(e) => {
            setQuery(e.currentTarget.value);
            setPage(1);
          }}
        />
        <Indicator label={selected.length} size={16} disabled={selected.length === 0} color="teal">
          <ActionIcon size={42} variant="default" aria-label="筛选" onClick={onFilterOpen}>
            <IconAdjustmentsHorizontal size={20} />
          </ActionIcon>
        </Indicator>
      </div>
      <div className="r-summary">
        <span>
          {meta ? (
            <>
              {meta.scanning ? "已发现" : "共"} {meta.found.toLocaleString()} 部
              {meta.total !== meta.found && ` · 筛选结果 ${meta.total.toLocaleString()} 部`}
              {meta.scanning && <Loader size={12} ml={8} />}
            </>
          ) : (
            loading && <Loader size={12} />
          )}
        </span>
        {/* 手机ではネイティブの選択ダイアログが最も操作しやすい。 */}
        <NativeSelect
          aria-label="排序方式"
          size="xs"
          value={sort}
          data={SORT_OPTIONS}
          onChange={(e) => {
            const next = e.currentTarget.value as SortKey;
            setSort(next);
            saveSort(next);
            setPage(1);
            window.scrollTo({ top: 0 });
          }}
        />
      </div>
      {selected.length > 0 && (
        <div className="r-chips">
          {selected.map((tag) => (
            <Button key={tag} size="compact-sm" variant="light" rightSection={<IconX size={12} />} onClick={() => resetTo(selected.filter((t) => t !== tag))}>
              {tag}
            </Button>
          ))}
        </div>
      )}
      {error && (
        <Text c="red" size="sm" my="sm">
          {error}
        </Text>
      )}
      <div className="r-grid">
        {entries.map((entry) => {
          const p = progress[String(entry.id)];
          const title = titleOf(entry);
          return (
            <button key={entry.id} type="button" className="r-card" onClick={() => onOpen(entry.id)} aria-label={title}>
              <div className="r-cover">
                <img src={`/api/v1/reader/works/${entry.id}/cover`} alt="" loading="lazy" />
                <span className="r-badge">{entry.pages} 页</span>
                {p && p.page > 1 && (
                  <span className="r-progress" style={{ width: `${Math.min(100, (p.page / Math.max(1, p.total)) * 100)}%` }} />
                )}
              </div>
              <div className="r-title">{title}</div>
              <div className="r-sub">
                {entry.artists.length ? entry.artists.join("、") : entry.language}
                {p && p.page > 1 && ` · 读到 ${p.page}/${p.total}`}
              </div>
            </button>
          );
        })}
      </div>
      {meta && entries.length === 0 && !loading && (
        <Text c="dimmed" ta="center" my="xl">
          {query || selected.length ? "没有匹配的作品" : meta.scanning ? "正在扫描下载目录…" : "没有发现作品"}
        </Text>
      )}
      <div ref={sentinel} className="r-more">
        {loading && entries.length > 0 && <Loader size="sm" />}
      </div>
      <Drawer opened={filterOpen} onClose={onFilterClose} position="bottom" size="75%" title="按标签筛选" padding="md">
        <Text size="sm" c="dimmed" mb="xs">
          选择多个标签时，需同时满足。
        </Text>
        <TextInput aria-label="搜索标签" placeholder="搜索标签" leftSection={<IconSearch size={18} />} value={tagQuery} onChange={(e) => setTagQuery(e.currentTarget.value)} />
        {selected.length > 0 && (
          <Group gap={6} mt="sm">
            {selected.map((tag) => (
              <Button key={tag} size="compact-md" variant="filled" rightSection={<IconX size={14} />} onClick={() => resetTo(selected.filter((t) => t !== tag))}>
                {tag}
              </Button>
            ))}
            <Button size="compact-md" variant="subtle" color="gray" onClick={() => resetTo([])}>
              清除全部
            </Button>
          </Group>
        )}
        <Group gap={6} mt="md">
          {tagOptions.map((tag) => (
            <Button key={tag.value} size="compact-md" variant="light" color={tag.value.startsWith("female:") ? "pink" : tag.value.startsWith("male:") ? "blue" : "gray"} onClick={() => resetTo([...selected, tag.value])}>
              {tag.value} · {tag.count}
            </Button>
          ))}
          {tagOptions.length === 0 && (
            <Text size="sm" c="dimmed">
              没有可选的标签
            </Text>
          )}
        </Group>
        <Button fullWidth mt="lg" onClick={onFilterClose}>
          查看 {meta?.total.toLocaleString() ?? 0} 部作品
        </Button>
      </Drawer>
    </div>
  );
}

export { tagValue };
