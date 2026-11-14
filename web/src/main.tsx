import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Alert,
  AppShell,
  Badge,
  Burger,
  Button,
  Container,
  Group,
  MantineProvider,
  Modal,
  MultiSelect,
  NavLink,
  NumberInput,
  Pagination,
  Paper,
  Select,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
  Title,
  createTheme,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { Notifications } from "@mantine/notifications";
import {
  IconBook2,
  IconCheck,
  IconDownload,
  IconFolder,
  IconPower,
  IconSearch,
  IconSettings,
} from "@tabler/icons-react";
import { api, Settings } from "./api";
import { report } from "./common";
import { DownloadedPane } from "./DownloadedPane";
import { DownloadStatusProvider } from "./downloadStatus";
import { LOCALES, LocaleSetting, savedSetting, setLocale, t } from "./i18n";
import { MobileLink } from "./MobileLink";
import { QueuePane } from "./QueuePane";
import { SearchPane } from "./SearchPane";
import { isActive } from "./common";
import { useService } from "./useService";
import "@mantine/core/styles.css";
import "@mantine/notifications/styles.css";
import "./styles.css";

// 部品の既定サイズを小さくして、余白を詰める。
const theme = createTheme({
  primaryColor: "teal",
  defaultRadius: "sm",
  fontFamily:
    'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
  fontSizes: { xs: "11px", sm: "12.5px", md: "13.5px", lg: "15px", xl: "17px" },
  spacing: { xs: "4px", sm: "6px", md: "10px", lg: "14px", xl: "18px" },
  components: {
    Button: Button.extend({ defaultProps: { size: "xs" } }),
    TextInput: TextInput.extend({ defaultProps: { size: "xs" } }),
    Select: Select.extend({ defaultProps: { size: "xs" } }),
    MultiSelect: MultiSelect.extend({ defaultProps: { size: "xs" } }),
    Pagination: Pagination.extend({ defaultProps: { size: "sm" } }),
    Badge: Badge.extend({ defaultProps: { size: "xs" } }),
  },
});

type Tab = "search" | "downloads" | "library";
const pages = [
  {
    id: "search" as Tab,
    label: t("搜索作品"),
    description: t("通过关键词、标签或作品 ID 查找内容。"),
    icon: IconSearch,
  },
  {
    id: "downloads" as Tab,
    label: t("下载队列"),
    description: t("管理下载进度，中断后可继续恢复。"),
    icon: IconDownload,
  },
  {
    id: "library" as Tab,
    label: t("已下载"),
    description: t("扫描下载目录，验证并补全缺失或损坏的页面。"),
    icon: IconFolder,
  },
];

function App() {
  const [tab, setTab] = useState<Tab>("search");
  const {
    state: serviceState,
    tasks,
    stop: stopService,
    refreshTasks,
  } = useService();
  const unavailable = serviceState === "stopping" || serviceState === "stopped";
  const [mobileOpened, { toggle: toggleMobile, close: closeMobile }] =
    useDisclosure();
  const [directory, setDirectory] = useState("");
  const [draftDirectory, setDraftDirectory] = useState("");
  // 並行数の既定値（サーバー側の既定と一致させる）。
  const [concurrency, setConcurrency] = useState({ tasks: 2, pages: 4 });
  const [draftTasks, setDraftTasks] = useState<number | string>(2);
  const [draftPages, setDraftPages] = useState<number | string>(4);
  const [draftLang, setDraftLang] = useState<LocaleSetting>(savedSetting);
  const [settings, { open, close }] = useDisclosure();

  useEffect(() => {
    api<Settings>("/settings")
      .then((c) => {
        setDirectory(c.downloadDir);
        setConcurrency({
          tasks: c.taskConcurrency || 2,
          pages: c.pageConcurrency || 4,
        });
      })
      .catch(report);
  }, []);
  useEffect(() => {
    if (unavailable) {
      close();
      closeMobile();
    }
  }, [unavailable]);
  // 「/」で現在表示中の検索欄にフォーカスする。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey) return;
      if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;
      const input = Array.from(
        document.querySelectorAll<HTMLElement>("[data-hotkey-search]"),
      ).find((el) => el.offsetParent !== null);
      if (input) {
        e.preventDefault();
        input.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const active = tasks.filter(isActive).length;
  const currentPage = pages.find((p) => p.id === tab) || pages[0];
  const serviceLabel = {
    online: t("本地服务已连接"),
    connecting: t("正在连接服务"),
    stopping: t("正在停止服务"),
    stopped: t("服务已停止"),
  }[serviceState];

  return (
    <DownloadStatusProvider tasks={tasks}>
    <AppShell
      header={{ height: 64 }}
      navbar={{
        width: 236,
        breakpoint: "sm",
        collapsed: { mobile: !mobileOpened },
      }}
      padding={{ base: "xs", sm: "md" }}
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
              aria-label={mobileOpened ? t("关闭导航") : t("打开导航")}
            />
            <ThemeIcon size={34} radius="md" variant="light">
              <IconBook2 />
            </ThemeIcon>
            <Text fw={750} size="md">
              Hitomi <span className="brand-local">Local</span>
            </Text>
          </Group>
          <Group>
            <Badge
              visibleFrom="md"
              variant="dot"
              size="sm"
              color={
                serviceState === "online"
                  ? "teal"
                  : serviceState === "stopped"
                    ? "gray"
                    : "orange"
              }
            >
              {serviceLabel}
            </Badge>
            <Button
              variant="light"
              color="red"
              leftSection={<IconPower size={15} />}
              loading={serviceState === "stopping"}
              disabled={serviceState === "stopped"}
              onClick={() => stopService().catch(report)}
            >
              {t("停止服务")}
            </Button>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar p="md">
        <Stack h="100%" gap="lg">
          <div>
            <Text size="xs" c="dimmed" fw={600} mb="sm" px="sm">
              {t("工作区")}
            </Text>
            <Stack gap={4}>
              {pages.map((p) => (
                <NavLink
                  key={p.id}
                  component="button"
                  type="button"
                  label={p.label}
                  leftSection={<p.icon size={19} />}
                  rightSection={
                    p.id === "downloads" && active > 0 ? (
                      <Badge variant="light">{active}</Badge>
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
                {t("下载目录")}
              </Text>
              <Text size="xs" className="path" lineClamp={3} title={directory}>
                {directory || t("正在加载…")}
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
                setDraftTasks(concurrency.tasks);
                setDraftPages(concurrency.pages);
                setDraftLang(savedSetting());
                closeMobile();
                open();
              }}
            >
              {t("设置")}
            </Button>
            <Text size="xs" c="dimmed" px="sm">
              Hitomi Local · {t("本地工作区")}
            </Text>
          </Stack>
        </Stack>
      </AppShell.Navbar>
      <AppShell.Main>
        <Container fluid p={0}>
          <div className="page-heading">
            <Title order={1}>{currentPage.label}</Title>
            <Text c="dimmed" size="xs">
              {currentPage.description}
            </Text>
          </div>
          {unavailable && (
            <Alert
              color={serviceState === "stopping" ? "orange" : "gray"}
              icon={<IconPower size={18} />}
              title={
                serviceState === "stopping" ? t("正在停止服务") : t("服务已停止")
              }
              mb="md"
            >
              {t("任务会保留已下载页面和进度。可以关闭此页面；再次使用时请重新运行 CLI 并打开新的启动链接。")}
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
            {/* 検索結果を保持するため、検索ペインだけは非表示にして残す。 */}
            <div hidden={tab !== "search"}>
              <SearchPane disabled={unavailable} />
            </div>
            {tab === "downloads" && (
              <QueuePane tasks={tasks} refreshTasks={refreshTasks} />
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
      <Modal opened={settings} onClose={close} title={t("设置")} centered size="sm">
        <Stack gap="sm">
          <TextInput
            label={t("下载目录")}
            description={t("填写本机绝对路径，更改后重新扫描已下载内容。")}
            value={draftDirectory}
            onChange={(e) => setDraftDirectory(e.currentTarget.value)}
          />
          <NumberInput
            label={t("同时处理的任务数")}
            description={t("同时下载、验证、更新的作品数（1–8），对排队中的任务立即生效。")}
            min={1}
            max={8}
            allowDecimal={false}
            allowNegative={false}
            value={draftTasks}
            onChange={setDraftTasks}
          />
          <NumberInput
            label={t("每个任务的并发文件数")}
            description={t("一个任务内同时取图的页数（1–16），对新开始或继续的任务生效。")}
            min={1}
            max={16}
            allowDecimal={false}
            allowNegative={false}
            value={draftPages}
            onChange={setDraftPages}
          />
          <Text size="xs" c="dimmed">
            {t("对 Hitomi 的请求仍有全局间隔限制，并发数调得过高不会成比例提速，也可能触发限流。")}
          </Text>
          <Select
            label={t("界面语言")}
            description={t("同时用于终端输出和服务端提示。选择“自动”时，界面跟随浏览器语言，终端输出跟随系统语言。")}
            allowDeselect={false}
            value={draftLang}
            onChange={(v) => v && setDraftLang(v as LocaleSetting)}
            data={[{ value: "auto", label: t("自动") }, ...LOCALES]}
          />
          <MobileLink />
          <Alert
            color="teal"
            p="xs"
            icon={<IconCheck size={16} />}
            title={t("质量优先")}
          >
            {t("优先获取源文件。仅当源文件不可用时尝试可用的衍生格式，具体格式及回退原因保存在 metadata 中。")}
          </Alert>
          <Text size="xs" c="dimmed">
            {t("运行中的任务需要先暂停。Proxy 可在启动时通过 --proxy 指定。")}
          </Text>
          <Button
            onClick={async () => {
              try {
                const c = await api<Settings>(
                  "/settings",
                  {
                    downloadDir: draftDirectory,
                    taskConcurrency: Number(draftTasks) || 0,
                    pageConcurrency: Number(draftPages) || 0,
                    // サーバー側（ターミナル出力と API メッセージ）の言語も同じにする。
                    language: draftLang === "auto" ? "auto" : draftLang,
                  },
                  "PUT",
                );
                setDirectory(c.downloadDir);
                setConcurrency({
                  tasks: c.taskConcurrency || 2,
                  pages: c.pageConcurrency || 4,
                });
                close();
                if (draftLang !== savedSetting()) setLocale(draftLang);
              } catch (e) {
                report(e);
              }
            }}
          >
            {t("保存设置")}
          </Button>
        </Stack>
      </Modal>
    </AppShell>
    </DownloadStatusProvider>
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
