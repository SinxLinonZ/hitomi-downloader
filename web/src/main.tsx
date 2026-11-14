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
import { api } from "./api";
import { report } from "./common";
import { DownloadedPane } from "./DownloadedPane";
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
    label: "搜索作品",
    description: "通过关键词、标签或作品 ID 查找内容。",
    icon: IconSearch,
  },
  {
    id: "downloads" as Tab,
    label: "下载队列",
    description: "管理下载进度，中断后可继续恢复。",
    icon: IconDownload,
  },
  {
    id: "library" as Tab,
    label: "已下载",
    description: "扫描下载目录，验证并补全缺失或损坏的页面。",
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
  const [settings, { open, close }] = useDisclosure();

  useEffect(() => {
    api<{ downloadDir: string }>("/settings")
      .then((c) => setDirectory(c.downloadDir))
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
    online: "本地服务已连接",
    connecting: "正在连接服务",
    stopping: "正在停止服务",
    stopped: "服务已停止",
  }[serviceState];

  return (
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
              aria-label={mobileOpened ? "关闭导航" : "打开导航"}
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
                serviceState === "stopping" ? "正在停止服务" : "服务已停止"
              }
              mb="md"
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
      <Modal opened={settings} onClose={close} title="设置" centered size="sm">
        <Stack gap="sm">
          <TextInput
            label="下载目录"
            description="填写本机绝对路径，更改后重新扫描已下载内容。"
            value={draftDirectory}
            onChange={(e) => setDraftDirectory(e.currentTarget.value)}
          />
          <Alert
            color="teal"
            p="xs"
            icon={<IconCheck size={16} />}
            title="质量优先"
          >
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
    </AppShell>
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
