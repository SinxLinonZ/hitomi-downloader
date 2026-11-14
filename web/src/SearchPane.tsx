import { useEffect, useRef, useState } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Drawer,
  Group,
  Loader,
  Pagination,
  Paper,
  Text,
  TextInput,
  Title,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconArrowDown,
  IconArrowLeft,
  IconDownload,
  IconInfoCircle,
  IconRefresh,
  IconSearch,
  IconX,
} from "@tabler/icons-react";
import { api, Gallery, Search } from "./api";
import { Cover, Empty, errorText, report, titles } from "./common";
import { GalleryCard } from "./GalleryCard";
import { WorkDrawer } from "./WorkDrawer";
import { t } from "./i18n";

const hint = () => t("多个词取交集；标签中的空格用下划线连接，前缀 - 排除标签。");

export function SearchPane({ disabled }: { disabled: boolean }) {
  const [query, setQuery] = useState("");
  const [idInput, setIDInput] = useState("");
  const [result, setResult] = useState<Search>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [picked, setPicked] = useState<Gallery>();
  const [coverKey, setCoverKey] = useState(0);
  const request = useRef<AbortController | null>(null);
  const lastSearch = useRef<{ page: number; session?: string }>({ page: 1 });

  useEffect(() => () => request.current?.abort(), []);
  useEffect(() => {
    if (disabled) {
      request.current?.abort();
      setBusy(false);
      setPicked(undefined);
    }
  }, [disabled]);

  async function search(page = 1, session?: string) {
    lastSearch.current = { page, session };
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setBusy(true);
    setError("");
    try {
      const params = new URLSearchParams({ q: query, page: String(page) });
      if (session) params.set("session", session);
      const r = await api<Search>(
        `/search?${params}`,
        undefined,
        undefined,
        controller.signal,
      );
      if (!controller.signal.aborted) setResult(r);
    } catch (e) {
      if (!controller.signal.aborted) setError(errorText(e));
    } finally {
      if (request.current === controller) setBusy(false);
    }
  }

  async function openById() {
    const match = idInput.trim().match(/^(\d+)$|-(\d+)\.html(?:[?#].*)?$/);
    if (!match) {
      report(new Error(t("请输入有效 ID 或作品链接")));
      return;
    }
    try {
      setPicked(await api<Gallery>(`/galleries/${match[1] || match[2]}`));
    } catch (e) {
      report(e);
    }
  }

  return (
    <>
      <div className="sticky-head">
        <div className="toolbar">
          <form
            style={{ display: "contents" }}
            onSubmit={(e) => {
              e.preventDefault();
              void search();
            }}
          >
            <TextInput
              className="grow"
              aria-label={t("关键词与标签")}
              placeholder={t("关键词 · artist:name · language:japanese · -tag:name　（按 / 聚焦）")}
              data-hotkey-search
              leftSection={<IconSearch size={14} />}
              rightSection={
                <Tooltip label={hint()} multiline w={240}>
                  <IconInfoCircle size={14} color="var(--dim)" />
                </Tooltip>
              }
              value={query}
              onChange={(e) => setQuery(e.currentTarget.value)}
            />
            <Button type="submit" loading={busy}>
              {t("搜索")}
            </Button>
            {busy && (
              <Button
                variant="subtle"
                color="gray"
                onClick={() => {
                  request.current?.abort();
                  setBusy(false);
                }}
              >
                {t("取消")}
              </Button>
            )}
          </form>
          <TextInput
            aria-label={t("作品 ID 或链接")}
            placeholder={t("作品 ID / 链接")}
            w={170}
            value={idInput}
            onChange={(e) => setIDInput(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void openById();
            }}
          />
          <Button variant="light" onClick={openById}>
            {t("打开作品")}
          </Button>
        </div>
        {result && (
          <div className="subbar">
            <span>
              {t("找到 {total} 个作品 · 第 {page} 页", { total: result.total.toLocaleString(), page: result.page })}
            </span>
            <Tooltip label={t("刷新本页")}>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="sm"
                aria-label={t("刷新本页")}
                onClick={() => {
                  setCoverKey((k) => k + 1);
                  void search(result.page, result.session);
                }}
              >
                <IconRefresh size={15} />
              </ActionIcon>
            </Tooltip>
          </div>
        )}
      </div>
      {error && (
        <Alert
          color="red"
          py={6}
          mb="xs"
          icon={<IconX size={16} />}
          title={t("搜索暂时无法完成")}
        >
          {error}
          <Button
            variant="subtle"
            color="red"
            ml="sm"
            onClick={() =>
              search(lastSearch.current.page, lastSearch.current.session)
            }
          >
            {t("重试")}
          </Button>
        </Alert>
      )}
      {result ? (
        <>
          <div className="gallery-grid">
            {result.items.map((item) => {
              const g = item.gallery;
              if (!g)
                return (
                  <Paper withBorder key={item.id} className="g-card">
                    <div className="g-error">
                      <Text size="xs" c="dimmed">
                        #{item.id}
                      </Text>
                      <Text size="xs" c="red">
                        {item.error}
                      </Text>
                      <Button
                        variant="light"
                        onClick={() => search(result.page, result.session)}
                      >
                        {t("重试此页信息")}
                      </Button>
                    </div>
                  </Paper>
                );
              return (
                <GalleryCard
                  key={item.id}
                  id={item.id}
                  g={g}
                  coverKey={coverKey}
                  onOpen={setPicked}
                />
              );
            })}
          </div>
          {result.total === 0 && <Empty text={t("没有匹配结果，试试其他关键词。")} />}
          {result.total > 24 && (
            <div className="pager">
              <Pagination
                total={Math.ceil(result.total / 24)}
                value={result.page}
                onChange={(p) => search(p, result.session)}
                disabled={busy}
                size="sm"
              />
            </div>
          )}
        </>
      ) : (
        !busy && <Empty text={t("从一个关键词开始，或直接打开作品 ID。")} />
      )}
      <WorkDrawer
        opened={picked ? { gallery: picked } : undefined}
        onClose={() => setPicked(undefined)}
      />
    </>
  );
}
