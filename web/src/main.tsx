import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ActionIcon,
  Alert,
  AppShell,
  Badge,
  Burger,
  Button,
  Card,
  Checkbox,
  Container,
  Divider,
  Drawer,
  Group,
  Image,
  MantineProvider,
  Modal,
  NavLink,
  Pagination,
  Paper,
  Progress,
  ScrollArea,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
  Title,
  createTheme,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { Notifications, notifications } from "@mantine/notifications";
import {
  IconArrowDown,
  IconBook2,
  IconCheck,
  IconDownload,
  IconFolder,
  IconPower,
  IconPlayerPause,
  IconPlayerPlay,
  IconTrash,
  IconRefresh,
  IconSearch,
  IconSettings,
  IconX,
} from "@tabler/icons-react";
import {
  api,
  Gallery,
  Search,
  stateLabels,
  Task,
  TaskBatchResult,
} from "./api";
import { DownloadedPane } from "./DownloadedPane";
import { useService } from "./useService";
import "@mantine/core/styles.css";
import "@mantine/notifications/styles.css";
import "./styles.css";

const theme = createTheme({
  primaryColor: "teal",
  defaultRadius: "md",
  fontFamily:
    'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
});
function App() {
  const [tab, setTab] = useState<string | null>("search");
  const [query, setQuery] = useState("");
  const [result, setResult] = useState<Search>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const {
    state: serviceState,
    tasks,
    stop: stopService,
    refreshTasks,
  } = useService();
  const [hideCompleted, setHideCompleted] = useState(false);
  const [queueAction, setQueueAction] = useState<string | null>(null);
  const unavailable = serviceState === "stopping" || serviceState === "stopped";
  const [mobileOpened, { toggle: toggleMobile, close: closeMobile }] =
    useDisclosure();
  const [directory, setDirectory] = useState("");
  const [draftDirectory, setDraftDirectory] = useState("");
  const [picked, setPicked] = useState<Gallery>();
  const [settings, { open, close }] = useDisclosure();
  const [detail, setDetail] = useState<Task>();
  const [idInput, setIDInput] = useState("");
  const request = useRef<AbortController | null>(null);
  const [coverKey, setCoverKey] = useState(0);
  const lastSearch = useRef<{ page: number; session?: string }>({ page: 1 });
  const report = (e: unknown) =>
    notifications.show({
      color: "red",
      title: "操作未完成",
      message: e instanceof Error ? e.message : String(e),
      autoClose: false,
    });
  useEffect(() => {
    api<{ downloadDir: string }>("/settings")
      .then((c) => setDirectory(c.downloadDir))
      .catch(report);
    return () => {
      request.current?.abort();
    };
  }, []);
  useEffect(() => {
    if (unavailable) {
      request.current?.abort();
      setBusy(false);
      close();
      closeMobile();
      setPicked(undefined);
      setDetail(undefined);
    }
  }, [unavailable]);
  useEffect(() => {
    if (detail && !tasks.some((t) => t.id === detail.id)) setDetail(undefined);
  }, [tasks, detail]);
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
      if (!controller.signal.aborted)
        setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (request.current === controller) setBusy(false);
    }
  }
  async function add(id: number | string) {
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
  async function control(id: number, action: string) {
    try {
      await api(`/downloads/${id}/${action}`, {});
    } catch (e) {
      report(e);
    }
  }
  async function controlQueue(
    action: "pause-all" | "resume-all" | "clear-completed",
  ) {
    setQueueAction(action);
    try {
      const result = await api<TaskBatchResult>(`/downloads/${action}`, {});
      await refreshTasks();
      const problems = Object.entries(result.errors);
      const message =
        action === "clear-completed"
          ? `已移除 ${result.affected} 个已完成任务，下载文件保持不变。`
          : action === "pause-all"
            ? `已暂停 ${result.affected} 个任务。`
            : `已开始 ${result.affected} 个未完成任务。`;
      notifications.show({
        color: problems.length ? "orange" : "teal",
        title: problems.length ? "部分任务未能处理" : "队列已更新",
        message:
          message +
          (problems.length ? ` ${problems.length} 个任务处理失败。` : ""),
      });
      if (problems.length)
        report(
          new Error(
            problems
              .slice(0, 5)
              .map(([id, error]) => `#${id}: ${error}`)
              .join("\n") +
              (problems.length > 5
                ? `\n另有 ${problems.length - 5} 项错误。`
                : ""),
          ),
        );
    } catch (e) {
      report(e);
    } finally {
      setQueueAction(null);
    }
  }
  const active = tasks.filter((t) =>
    ["running", "queued"].includes(t.state),
  ).length;
  const completed = tasks.filter((t) => t.state === "completed").length;
  const resumable = tasks.filter((t) =>
    ["paused", "failed", "cancelled"].includes(t.state),
  ).length;
  const visibleTasks = hideCompleted
    ? tasks.filter((t) => t.state !== "completed")
    : tasks;
  const pages = [
    {
      id: "search",
      label: "搜索作品",
      description: "通过关键词、标签或作品 ID 查找内容。",
      icon: IconSearch,
    },
    {
      id: "downloads",
      label: "下载队列",
      description: "管理下载进度，中断后可继续恢复。",
      icon: IconDownload,
    },
    {
      id: "library",
      label: "已下载",
      description: "扫描下载目录，验证并补全缺失或损坏的页面。",
      icon: IconFolder,
    },
  ];
  const currentPage = pages.find((p) => p.id === tab) || pages[0];
  return (
    <AppShell
      header={{ height: 64 }}
      navbar={{
        width: 236,
        breakpoint: "sm",
        collapsed: { mobile: !mobileOpened },
      }}
      padding={{ base: "md", sm: "xl" }}
    >
      <AppShell.Header>
        <Group
          h="100%"
          justify="space-between"
          px={{ base: "md", sm: "lg" }}
          wrap="nowrap"
        >
          <Group gap="sm">
            <Burger
              opened={mobileOpened}
              onClick={toggleMobile}
              hiddenFrom="sm"
              size="sm"
              aria-label={mobileOpened ? "关闭导航" : "打开导航"}
            />
            <ThemeIcon size={34} radius="md" variant="light">
              <IconBook2 />
            </ThemeIcon>
            <div>
              <Text fw={750} size="md">
                Hitomi <span className="brand-local">Local</span>
              </Text>
            </div>
          </Group>
          <Group>
            <Badge
              visibleFrom="md"
              variant="dot"
              color={
                serviceState === "online"
                  ? "teal"
                  : serviceState === "stopped"
                    ? "gray"
                    : "orange"
              }
            >
              {
                {
                  online: "本地服务已连接",
                  connecting: "正在连接服务",
                  stopping: "正在停止服务",
                  stopped: "服务已停止",
                }[serviceState]
              }
            </Badge>
            <Button
              size="xs"
              variant="light"
              color="red"
              leftSection={<IconPower size={15} />}
              loading={serviceState === "stopping"}
              disabled={serviceState === "stopped"}
              onClick={() => stopService().catch(report)}
            >
              停止服务
            </Button>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar p="md">
        <Stack h="100%" gap="lg">
          <div>
            <Text size="xs" c="dimmed" fw={600} mb="sm" px="sm">
              工作区
            </Text>
            <Stack gap={5}>
              {pages.map((p) => (
                <NavLink
                  key={p.id}
                  component="button"
                  type="button"
                  label={p.label}
                  leftSection={<p.icon size={19} />}
                  rightSection={
                    p.id === "downloads" && active > 0 ? (
                      <Badge size="xs" variant="light">
                        {active}
                      </Badge>
                    ) : undefined
                  }
                  active={tab === p.id}
                  disabled={unavailable}
                  onClick={() => {
                    setTab(p.id);
                    closeMobile();
                  }}
                  className="workspace-nav"
                />
              ))}
            </Stack>
          </div>
          <Stack mt="auto" gap="md">
            <Paper p="sm" className="directory-summary">
              <Text size="xs" c="dimmed" mb={5}>
                下载目录
              </Text>
              <Text size="xs" className="path" lineClamp={3} title={directory}>
                {directory || "正在加载…"}
              </Text>
            </Paper>
            <Button
              fullWidth
              justify="flex-start"
              variant="subtle"
              color="gray"
              disabled={unavailable}
              leftSection={<IconSettings size={18} />}
              onClick={() => {
                setDraftDirectory(directory);
                closeMobile();
                open();
              }}
            >
              设置
            </Button>
            <Text size="xs" c="dimmed" px="sm">
              Hitomi Local · 本地工作区
            </Text>
          </Stack>
        </Stack>
      </AppShell.Navbar>
      <AppShell.Main>
        <Container size="xl">
          <div className="page-heading">
            <Text size="xs" c="dimmed" mb={6}>
              工作区 / {currentPage.label}
            </Text>
            <Title order={1}>{currentPage.label}</Title>
            <Text c="dimmed" mt="xs" size="sm">
              {currentPage.description}
            </Text>
          </div>
          {unavailable && (
            <Alert
              color={serviceState === "stopping" ? "orange" : "gray"}
              icon={<IconPower size={18} />}
              title={
                serviceState === "stopping" ? "正在停止服务" : "服务已停止"
              }
              mb="xl"
            >
              任务会保留已下载页面和进度。可以关闭此页面；再次使用时请重新运行
              CLI 并打开新的启动链接。
            </Alert>
          )}
          <div
            inert={unavailable}
            className={
              unavailable
                ? "workspace-content workspace-disabled"
                : "workspace-content"
            }
          >
            {tab === "search" && (
              <Stack gap="xl">
                <Paper withBorder p="lg">
                  <form
                    onSubmit={(e) => {
                      e.preventDefault();
                      void search();
                    }}
                  >
                    <Group align="end">
                      <TextInput
                        style={{ flex: 1 }}
                        label="关键词与标签"
                        placeholder="标题关键词、artist:name、language:japanese、-tag:name"
                        size="md"
                        leftSection={<IconSearch size={18} />}
                        value={query}
                        onChange={(e) => setQuery(e.currentTarget.value)}
                      />
                      <Button size="md" type="submit" loading={busy}>
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
                    </Group>
                    <Text mt="xs" c="dimmed" size="xs">
                      多个词取交集；标签中的空格用下划线连接，前缀 - 排除标签。
                    </Text>
                  </form>
                  <Divider my="md" />
                  <Group>
                    <TextInput
                      placeholder="输入 gallery ID 或 Hitomi 链接"
                      value={idInput}
                      onChange={(e) => setIDInput(e.currentTarget.value)}
                      style={{ flex: 1 }}
                    />
                    <Button
                      variant="light"
                      onClick={async () => {
                        const match = idInput
                          .trim()
                          .match(/^(\d+)$|-(\d+)\.html(?:[?#].*)?$/);
                        if (!match) {
                          report(new Error("请输入有效 ID 或作品链接"));
                          return;
                        }
                        try {
                          setPicked(
                            await api<Gallery>(
                              `/galleries/${match[1] || match[2]}`,
                            ),
                          );
                        } catch (e) {
                          report(e);
                        }
                      }}
                    >
                      打开作品
                    </Button>
                  </Group>
                </Paper>
                {error && (
                  <Alert color="red" title="搜索暂时无法完成" icon={<IconX />}>
                    {error}
                    <Button
                      variant="subtle"
                      color="red"
                      size="xs"
                      ml="md"
                      onClick={() =>
                        search(
                          lastSearch.current.page,
                          lastSearch.current.session,
                        )
                      }
                    >
                      重试
                    </Button>
                  </Alert>
                )}
                {result ? (
                  <>
                    <Group justify="space-between">
                      <Text fw={600}>
                        找到 {result.total.toLocaleString()} 个作品
                      </Text>
                      <Group>
                        <Text size="sm" c="dimmed">
                          第 {result.page} 页
                        </Text>
                        <ActionIcon
                          variant="subtle"
                          onClick={() => {
                            setCoverKey((k) => k + 1);
                            void search(result.page, result.session);
                          }}
                        >
                          <IconRefresh size={18} />
                        </ActionIcon>
                      </Group>
                    </Group>
                    <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>
                      {result.items.map((item) => (
                        <Card key={item.id} withBorder p="md">
                          {item.gallery ? (
                            <>
                              <Group wrap="nowrap" align="start">
                                <Image
                                  className="cover"
                                  key={`${item.id}-${coverKey}`}
                                  src={`/api/v1/covers/${item.id}?v=${coverKey}`}
                                  w={94}
                                  h={132}
                                  radius="sm"
                                  fit="contain"
                                  fallbackSrc="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='94' height='132'%3E%3Crect fill='%232a2e35' width='94' height='132'/%3E%3C/svg%3E"
                                />
                                <Stack gap={8} style={{ minWidth: 0 }}>
                                  <Badge size="xs" variant="light">
                                    #{item.id}
                                  </Badge>
                                  <Text
                                    className="gallery-title"
                                    fw={650}
                                    lineClamp={3}
                                    title={
                                      item.gallery.japanese_title?.trim() ||
                                      item.gallery.title
                                    }
                                    onClick={() => setPicked(item.gallery)}
                                  >
                                    {item.gallery.japanese_title?.trim() ||
                                      item.gallery.title}
                                  </Text>
                                  {item.gallery.japanese_title?.trim() &&
                                    item.gallery.title.trim() &&
                                    item.gallery.japanese_title.trim() !==
                                      item.gallery.title.trim() && (
                                      <Text
                                        size="xs"
                                        c="dimmed"
                                        lineClamp={2}
                                        className="work-title-secondary"
                                      >
                                        {item.gallery.title}
                                      </Text>
                                    )}
                                  <Text size="xs" c="dimmed">
                                    {item.gallery.language || "未知语言"} ·{" "}
                                    {item.gallery.files.length} 页
                                  </Text>
                                </Stack>
                              </Group>
                              <Group mt="lg" justify="space-between">
                                <Button
                                  size="xs"
                                  variant="subtle"
                                  color="gray"
                                  onClick={() => setPicked(item.gallery)}
                                >
                                  查看详情
                                </Button>
                                <Button
                                  size="xs"
                                  variant="light"
                                  leftSection={<IconArrowDown size={14} />}
                                  onClick={() => add(item.id)}
                                >
                                  下载
                                </Button>
                              </Group>
                            </>
                          ) : (
                            <Stack>
                              <Text c="dimmed">#{item.id}</Text>
                              <Text size="sm" c="red">
                                {item.error}
                              </Text>
                              <Button
                                size="xs"
                                variant="light"
                                onClick={() =>
                                  search(result.page, result.session)
                                }
                              >
                                重试此页信息
                              </Button>
                            </Stack>
                          )}
                        </Card>
                      ))}
                    </SimpleGrid>
                    {result.total === 0 && (
                      <Empty text="没有匹配结果，试试其他关键词。" />
                    )}
                    {result.total > 24 && (
                      <Pagination
                        total={Math.ceil(result.total / 24)}
                        value={result.page}
                        onChange={(p) => search(p, result.session)}
                        disabled={busy}
                        mx="auto"
                      />
                    )}
                  </>
                ) : (
                  !busy && (
                    <Empty text="从一个关键词开始，或直接打开作品 ID。" />
                  )
                )}
              </Stack>
            )}
            {tab === "downloads" && (
              <Stack>
                <Group justify="space-between">
                  <Text size="sm" c="dimmed">
                    {active} 个任务正在处理或等待
                  </Text>
                  <Badge variant="light">
                    显示 {visibleTasks.length} / {tasks.length} 个任务
                  </Badge>
                </Group>
                <Paper withBorder p="md">
                  <Stack gap="xs">
                    <Group justify="space-between">
                      <Group gap="xs">
                        <Button
                          size="sm"
                          variant="light"
                          leftSection={<IconPlayerPlay size={16} />}
                          title="恢复暂停、失败和已取消的未完成任务，跳过已完成任务"
                          disabled={queueAction !== null || resumable === 0}
                          loading={queueAction === "resume-all"}
                          onClick={() => controlQueue("resume-all")}
                        >
                          全部开始
                        </Button>
                        <Button
                          size="sm"
                          variant="default"
                          leftSection={<IconPlayerPause size={16} />}
                          disabled={queueAction !== null || active === 0}
                          loading={queueAction === "pause-all"}
                          onClick={() => controlQueue("pause-all")}
                        >
                          全部暂停
                        </Button>
                        <Button
                          size="sm"
                          variant="subtle"
                          color="gray"
                          leftSection={<IconTrash size={16} />}
                          title="仅移除任务记录，不删除下载文件或 metadata"
                          disabled={queueAction !== null || completed === 0}
                          loading={queueAction === "clear-completed"}
                          onClick={() => controlQueue("clear-completed")}
                        >
                          移除已完成任务（{completed}）
                        </Button>
                      </Group>
                      <Checkbox
                        label="隐藏已完成"
                        checked={hideCompleted}
                        onChange={(e) =>
                          setHideCompleted(e.currentTarget.checked)
                        }
                      />
                    </Group>
                    <Text size="xs" c="dimmed">
                      移除已完成仅清理下载 / 处理任务记录，不删除下载文件和作品
                      metadata。
                    </Text>
                  </Stack>
                </Paper>
                {tasks.length === 0 && (
                  <Empty text="下载队列还是空的，去搜索中添加作品吧。" />
                )}
                {tasks.length > 0 && visibleTasks.length === 0 && (
                  <Empty text="已完成任务已隐藏，取消勾选可重新显示。" />
                )}
                {visibleTasks.map((t) => (
                  <Paper key={t.id} withBorder p="lg">
                    <Group justify="space-between" align="start">
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <Group>
                          <Badge
                            variant="light"
                            color={
                              t.state === "failed"
                                ? "red"
                                : t.state === "completed"
                                  ? "teal"
                                  : "gray"
                            }
                          >
                            {stateLabels[t.state] || t.state}
                          </Badge>
                          <Text size="xs" c="dimmed">
                            {t.operation === "repair"
                              ? "验证并补全"
                              : t.operation === "migrate"
                                ? "迁移旧 metadata"
                                : "源文件下载"}{" "}
                            · #{t.id}
                          </Text>
                        </Group>
                        <Text fw={600} mt="sm">
                          {t.title || `作品 #${t.id}`}
                        </Text>
                      </div>
                      <Group gap="xs">
                        {["queued", "running"].includes(t.state) ? (
                          <Button
                            size="xs"
                            variant="light"
                            color="gray"
                            disabled={queueAction !== null}
                            onClick={() => control(t.id, "pause")}
                          >
                            暂停
                          </Button>
                        ) : (
                          t.state !== "completed" && (
                            <Button
                              size="xs"
                              variant="light"
                              disabled={queueAction !== null}
                              onClick={() => control(t.id, "resume")}
                            >
                              继续 / 重试
                            </Button>
                          )
                        )}
                        {t.state !== "completed" && t.state !== "cancelled" && (
                          <Button
                            size="xs"
                            variant="subtle"
                            color="gray"
                            disabled={queueAction !== null}
                            onClick={() => control(t.id, "cancel")}
                          >
                            取消
                          </Button>
                        )}
                        <Button
                          size="xs"
                          variant="subtle"
                          onClick={() => setDetail(t)}
                        >
                          详情
                        </Button>
                      </Group>
                    </Group>
                    <Progress
                      mt="md"
                      value={t.total ? (t.done / t.total) * 100 : 0}
                      animated={t.state === "running"}
                      color={t.state === "failed" ? "red" : "teal"}
                    />
                    <Group mt="xs" justify="space-between">
                      <Text size="xs" c="dimmed">
                        {t.done} / {t.total || "—"} 页
                      </Text>
                      {Object.keys(t.errors).length > 0 && (
                        <Text size="xs" c="red">
                          {Object.keys(t.errors).length} 页
                          {t.operation === "migrate" ? "待补全" : "失败"}
                        </Text>
                      )}
                    </Group>
                    {t.error && (
                      <Text mt="xs" size="sm" c="red">
                        {t.error}
                      </Text>
                    )}
                    {t.warning && (
                      <Text mt="xs" size="sm" c="orange">
                        {t.warning}
                      </Text>
                    )}
                  </Paper>
                ))}
              </Stack>
            )}
            {tab === "library" && (
              <DownloadedPane
                directory={directory}
                tasks={tasks}
                disabled={unavailable}
                onQueued={() => setTab("downloads")}
              />
            )}
          </div>
        </Container>
      </AppShell.Main>
      <Modal opened={settings} onClose={close} title="设置" centered>
        <Stack>
          <TextInput
            label="下载目录"
            description="填写本机绝对路径，更改后重新扫描已下载内容。"
            value={draftDirectory}
            onChange={(e) => setDraftDirectory(e.currentTarget.value)}
          />
          <Alert color="teal" icon={<IconCheck size={18} />} title="质量优先">
            优先获取源文件。仅当源文件不可用时尝试可用的衍生格式，具体格式及回退原因保存在
            metadata 中。
          </Alert>
          <Text size="xs" c="dimmed">
            运行中的任务需要先暂停。Proxy 可在启动时通过 --proxy 指定。
          </Text>
          <Button
            onClick={async () => {
              try {
                const c = await api<{ downloadDir: string }>(
                  "/settings",
                  { downloadDir: draftDirectory },
                  "PUT",
                );
                setDirectory(c.downloadDir);
                close();
              } catch (e) {
                report(e);
              }
            }}
          >
            保存设置
          </Button>
        </Stack>
      </Modal>
      <Drawer
        opened={!!picked}
        onClose={() => setPicked(undefined)}
        title="作品详情"
        position="right"
        size="lg"
      >
        {picked && (
          <Stack>
            <Image src={`/api/v1/covers/${picked.id}`} h={260} fit="contain" />
            <Title order={3}>
              {picked.japanese_title?.trim() || picked.title}
            </Title>
            {picked.japanese_title?.trim() &&
              picked.title.trim() &&
              picked.japanese_title.trim() !== picked.title.trim() && (
                <Text size="sm" c="dimmed" className="work-title-secondary">
                  {picked.title}
                </Text>
              )}
            <Group>
              <Badge>{picked.language || "未知语言"}</Badge>
              <Badge variant="light">{picked.files.length} 页</Badge>
              <Badge variant="light">{picked.type}</Badge>
            </Group>
            <Text size="sm">
              {picked.artists?.map((a) => a.artist).join("、")}
            </Text>
            <Group gap={6}>
              {picked.tags?.map((t, i) => (
                <Badge key={i} variant="outline" color="gray">
                  {t.tag}
                </Badge>
              ))}
            </Group>
            <Button
              leftSection={<IconDownload size={18} />}
              onClick={() => add(picked.id)}
            >
              加入下载队列
            </Button>
          </Stack>
        )}
      </Drawer>
      <Drawer
        opened={!!detail}
        onClose={() => setDetail(undefined)}
        title="任务详情"
        position="right"
      >
        {detail &&
          (() => {
            const t = tasks.find((t) => t.id === detail.id) || detail;
            return (
              <Stack>
                <Text fw={600}>{t.title || `#${t.id}`}</Text>
                <Text size="sm">
                  {stateLabels[t.state]} · {t.done}/{t.total} 页
                </Text>
                {t.error && <Alert color="red">{t.error}</Alert>}
                {t.warning && <Alert color="orange">{t.warning}</Alert>}
                <Text size="xs" className="path">
                  {t.directory}
                </Text>
                <ScrollArea>
                  {Object.entries(t.errors).map(([page, error]) => (
                    <Paper key={page} withBorder p="sm" mb="xs">
                      <Text size="sm" fw={600}>
                        第 {page} 页
                      </Text>
                      <Text size="xs" c="red">
                        {error}
                      </Text>
                    </Paper>
                  ))}
                </ScrollArea>
                <Text size="xs" c="dimmed">
                  {t.operation === "migrate"
                    ? "迁移仅处理现有图片；完成后可从“已下载”验证并补全。"
                    : "继续 / 重试会校验所有已完成文件，只补全缺失或损坏页面。"}
                </Text>
              </Stack>
            );
          })()}
      </Drawer>
    </AppShell>
  );
}
function Empty({ text }: { text: string }) {
  return (
    <Paper p={60} ta="center" className="empty">
      <ThemeIcon size={54} radius="xl" variant="light" color="gray">
        <IconBook2 size={26} />
      </ThemeIcon>
      <Text c="dimmed" mt="md" size="sm">
        {text}
      </Text>
    </Paper>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <MantineProvider theme={theme} forceColorScheme="dark">
      <Notifications position="top-right" />
      <App />
    </MantineProvider>
  </React.StrictMode>,
);
