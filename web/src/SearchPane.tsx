import { useEffect, useRef, useState } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Drawer,
  Group,
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
  IconDownload,
  IconInfoCircle,
  IconRefresh,
  IconSearch,
  IconX,
} from "@tabler/icons-react";
import { api, Gallery, Search } from "./api";
import { Cover, Empty, errorText, report, titles } from "./common";

const HINT = "多个词取交集；标签中的空格用下划线连接，前缀 - 排除标签。";

async function addDownload(id: number | string) {
  try {
    await api("/downloads", { id: Number(id) });
    notifications.show({
      title: "已加入下载队列",
      message: `作品 #${id} · 优先获取源文件`,
      color: "teal",
    });
  } catch (e) {
    report(e);
  }
}

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
      report(new Error("请输入有效 ID 或作品链接"));
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
              aria-label="关键词与标签"
              placeholder="关键词 · artist:name · language:japanese · -tag:name　（按 / 聚焦）"
              data-hotkey-search
              leftSection={<IconSearch size={14} />}
              rightSection={
                <Tooltip label={HINT} multiline w={240}>
                  <IconInfoCircle size={14} color="var(--dim)" />
                </Tooltip>
              }
              value={query}
              onChange={(e) => setQuery(e.currentTarget.value)}
            />
            <Button type="submit" loading={busy}>
              搜索
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
                取消
              </Button>
            )}
          </form>
          <TextInput
            aria-label="作品 ID 或链接"
            placeholder="作品 ID / 链接"
            w={170}
            value={idInput}
            onChange={(e) => setIDInput(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void openById();
            }}
          />
          <Button variant="light" onClick={openById}>
            打开作品
          </Button>
        </div>
        {result && (
          <div className="subbar">
            <span>
              找到 {result.total.toLocaleString()} 个作品 · 第 {result.page} 页
            </span>
            <Tooltip label="刷新本页">
              <ActionIcon
                variant="subtle"
                color="gray"
                size="sm"
                aria-label="刷新本页"
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
          title="搜索暂时无法完成"
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
            重试
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
                        重试此页信息
                      </Button>
                    </div>
                  </Paper>
                );
              const { main, sub } = titles(g.title, g.japanese_title);
              return (
                <Paper withBorder key={item.id} className="g-card">
                  <Cover
                    key={`${item.id}-${coverKey}`}
                    src={`/api/v1/covers/${item.id}?v=${coverKey}`}
                    alt={`${main} 封面`}
                  />
                  <span className="g-id">#{item.id}</span>
                  <div className="g-body">
                    <div
                      className="gallery-title"
                      title={sub ? `${main}\n${sub}` : main}
                      onClick={() => setPicked(g)}
                    >
                      {main}
                    </div>
                    <div className="g-meta">
                      <span>
                        {g.language || "未知语言"} · {g.files.length} 页
                      </span>
                      <Group gap={0} wrap="nowrap">
                        <Tooltip label="查看详情">
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            size="sm"
                            aria-label="查看详情"
                            onClick={() => setPicked(g)}
                          >
                            <IconInfoCircle size={15} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="加入下载队列">
                          <ActionIcon
                            variant="light"
                            size="sm"
                            aria-label={`下载 #${item.id}`}
                            onClick={() => addDownload(item.id)}
                          >
                            <IconArrowDown size={15} />
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    </div>
                  </div>
                </Paper>
              );
            })}
          </div>
          {result.total === 0 && <Empty text="没有匹配结果，试试其他关键词。" />}
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
        !busy && <Empty text="从一个关键词开始，或直接打开作品 ID。" />
      )}
      <Drawer
        opened={!!picked}
        onClose={() => setPicked(undefined)}
        title="作品详情"
        position="right"
        size="md"
        padding="md"
      >
        {picked && <WorkDetail g={picked} />}
      </Drawer>
    </>
  );
}

function WorkDetail({ g }: { g: Gallery }) {
  const { main, sub } = titles(g.title, g.japanese_title);
  const artists = g.artists?.map((a) => a.artist).join("、");
  return (
    <>
      <div className="detail-head">
        <Cover src={`/api/v1/covers/${g.id}`} alt={`${main} 封面`} />
        <div style={{ minWidth: 0, flex: 1 }}>
          <Title order={4} lh={1.35}>
            {main}
          </Title>
          {sub && (
            <Text size="xs" c="dimmed" mt={2} className="work-title-secondary" style={{ whiteSpace: "normal" }}>
              {sub}
            </Text>
          )}
          <Group gap={4} mt={6}>
            <Badge>{g.language || "未知语言"}</Badge>
            <Badge variant="light">{g.files.length} 页</Badge>
            <Badge variant="light">{g.type}</Badge>
            <Badge variant="outline" color="gray">
              #{g.id}
            </Badge>
          </Group>
          {artists && (
            <Text size="xs" mt={6}>
              {artists}
            </Text>
          )}
        </div>
      </div>
      <Group gap={4} mt="sm">
        {g.tags?.map((t, i) => (
          <Badge
            key={i}
            variant="outline"
            color={t.female ? "pink" : t.male ? "blue" : "gray"}
          >
            {t.female ? "female:" : t.male ? "male:" : ""}
            {t.tag}
          </Badge>
        ))}
      </Group>
      <Button
        fullWidth
        mt="md"
        leftSection={<IconDownload size={15} />}
        onClick={() => addDownload(g.id)}
      >
        加入下载队列
      </Button>
    </>
  );
}
