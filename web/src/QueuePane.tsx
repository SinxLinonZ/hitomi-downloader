import { useEffect, useState } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Drawer,
  Pagination,
  Paper,
  Progress,
  ScrollArea,
  SegmentedControl,
  Table,
  Text,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconListDetails,
  IconPlayerPause,
  IconPlayerPlay,
  IconTrash,
  IconX,
} from "@tabler/icons-react";
import { api, stateLabels, Task, TaskBatchResult } from "./api";
import {
  Empty,
  isActive,
  isResumable,
  operationLabel,
  report,
  taskTitle,
} from "./common";

const PAGE_SIZE = 100;
type Filter = "all" | "active" | "resumable" | "done";
const stateColor = (state: string) =>
  state === "failed"
    ? "red"
    : state === "completed"
      ? "teal"
      : state === "running"
        ? "blue"
        : "gray";

export function QueuePane({
  tasks,
  refreshTasks,
}: {
  tasks: Task[];
  refreshTasks: () => Promise<void>;
}) {
  const [filter, setFilter] = useState<Filter>("all");
  const [page, setPage] = useState(1);
  const [queueAction, setQueueAction] = useState<string | null>(null);
  const [detailId, setDetailId] = useState<number>();

  const active = tasks.filter(isActive).length;
  const resumable = tasks.filter(isResumable).length;
  const completed = tasks.filter((t) => t.state === "completed").length;
  const visible = tasks.filter((t) =>
    filter === "active"
      ? isActive(t)
      : filter === "resumable"
        ? isResumable(t)
        : filter === "done"
          ? t.state === "completed"
          : true,
  );
  const pages = Math.max(1, Math.ceil(visible.length / PAGE_SIZE));
  useEffect(() => {
    if (page > pages) setPage(pages);
  }, [page, pages]);
  const shown = visible.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);
  const detail = tasks.find((t) => t.id === detailId);

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

  return (
    <>
      <div className="sticky-head">
        <div className="toolbar">
          <SegmentedControl
            size="xs"
            value={filter}
            onChange={(v) => {
              setFilter(v as Filter);
              setPage(1);
            }}
            data={[
              { value: "all", label: `全部 ${tasks.length}` },
              { value: "active", label: `进行中 ${active}` },
              { value: "resumable", label: `待恢复 ${resumable}` },
              { value: "done", label: `已完成 ${completed}` },
            ]}
          />
          <Button
            variant="light"
            leftSection={<IconPlayerPlay size={14} />}
            title="恢复暂停、失败和已取消的未完成任务，跳过已完成任务"
            disabled={queueAction !== null || resumable === 0}
            loading={queueAction === "resume-all"}
            onClick={() => controlQueue("resume-all")}
          >
            全部开始
          </Button>
          <Button
            variant="default"
            leftSection={<IconPlayerPause size={14} />}
            disabled={queueAction !== null || active === 0}
            loading={queueAction === "pause-all"}
            onClick={() => controlQueue("pause-all")}
          >
            全部暂停
          </Button>
          <Button
            variant="subtle"
            color="gray"
            leftSection={<IconTrash size={14} />}
            title="仅移除任务记录，不删除下载文件或 metadata"
            disabled={queueAction !== null || completed === 0}
            loading={queueAction === "clear-completed"}
            onClick={() => controlQueue("clear-completed")}
          >
            移除已完成任务（{completed}）
          </Button>
        </div>
      </div>
      {tasks.length === 0 ? (
        <Empty text="下载队列还是空的，去搜索中添加作品吧。" />
      ) : shown.length === 0 ? (
        <Empty text="当前筛选下没有任务。" />
      ) : (
        <Paper withBorder>
          <Table.ScrollContainer minWidth={560}>
            <Table
              className="q-table"
              verticalSpacing={4}
              horizontalSpacing="sm"
              highlightOnHover
              fz="xs"
            >
              <Table.Tbody>
                {shown.map((t) => (
                  <Table.Tr key={t.id}>
                    <Table.Td w={64}>
                      <Badge size="sm" variant="light" color={stateColor(t.state)}>
                        {stateLabels[t.state] || t.state}
                      </Badge>
                    </Table.Td>
                    <Table.Td className="q-cell-title">
                      <div className="q-title" title={taskTitle(t)}>
                        <Text span fz="sm" fw={600}>
                          {taskTitle(t)}
                        </Text>{" "}
                        <Text span fz="xs" c="dimmed">
                          #{t.id} · {operationLabel(t.operation)}
                        </Text>
                      </div>
                      {t.error && (
                        <div className="q-note" style={{ color: "var(--mantine-color-red-4)" }} title={t.error}>
                          {t.error}
                        </div>
                      )}
                      {t.warning && (
                        <div className="q-note" style={{ color: "var(--mantine-color-orange-4)" }} title={t.warning}>
                          {t.warning}
                        </div>
                      )}
                      {Object.keys(t.errors).length > 0 && (
                        <div className="q-note" style={{ color: "var(--mantine-color-red-4)" }}>
                          {Object.keys(t.errors).length} 页
                          {t.operation === "migrate" ? "待补全" : "失败"}
                        </div>
                      )}
                    </Table.Td>
                    <Table.Td w={230}>
                      <div className="q-progress">
                        <Progress
                          size="xs"
                          value={t.total ? (t.done / t.total) * 100 : 0}
                          animated={t.state === "running"}
                          color={t.state === "failed" ? "red" : "teal"}
                        />
                        <span>
                          {t.done} / {t.total || "—"} 页
                        </span>
                      </div>
                    </Table.Td>
                    <Table.Td w={112} style={{ whiteSpace: "nowrap" }}>
                      {isActive(t) ? (
                        <Tooltip label="暂停">
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            aria-label="暂停"
                            disabled={queueAction !== null}
                            onClick={() => control(t.id, "pause")}
                          >
                            <IconPlayerPause size={16} />
                          </ActionIcon>
                        </Tooltip>
                      ) : (
                        t.state !== "completed" && (
                          <Tooltip label="继续 / 重试">
                            <ActionIcon
                              variant="subtle"
                              aria-label="继续 / 重试"
                              disabled={queueAction !== null}
                              onClick={() => control(t.id, "resume")}
                            >
                              <IconPlayerPlay size={16} />
                            </ActionIcon>
                          </Tooltip>
                        )
                      )}
                      {t.state !== "completed" && t.state !== "cancelled" && (
                        <Tooltip label="取消">
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            aria-label="取消"
                            disabled={queueAction !== null}
                            onClick={() => control(t.id, "cancel")}
                          >
                            <IconX size={16} />
                          </ActionIcon>
                        </Tooltip>
                      )}
                      <Tooltip label="详情">
                        <ActionIcon
                          variant="subtle"
                          aria-label="详情"
                          onClick={() => setDetailId(t.id)}
                        >
                          <IconListDetails size={16} />
                        </ActionIcon>
                      </Tooltip>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Paper>
      )}
      {pages > 1 && (
        <div className="pager">
          <Pagination total={pages} value={page} onChange={setPage} size="sm" />
        </div>
      )}
      <Drawer
        opened={!!detail}
        onClose={() => setDetailId(undefined)}
        title="任务详情"
        position="right"
        padding="md"
      >
        {detail && <TaskDetail t={detail} />}
      </Drawer>
    </>
  );
}

function TaskDetail({ t }: { t: Task }) {
  return (
    <>
      <Text fw={600}>{taskTitle(t)}</Text>
      <Text size="sm" mt={4}>
        {stateLabels[t.state]} · {t.done}/{t.total} 页
      </Text>
      {t.error && (
        <Alert color="red" mt="xs" p="xs">
          {t.error}
        </Alert>
      )}
      {t.warning && (
        <Alert color="orange" mt="xs" p="xs">
          {t.warning}
        </Alert>
      )}
      <Text size="xs" className="path" mt="xs">
        {t.directory}
      </Text>
      <ScrollArea mt="xs">
        {Object.entries(t.errors).map(([page, error]) => (
          <Paper key={page} withBorder px="xs" py={4} mb={4}>
            <Text size="xs" fw={600} span>
              第 {page} 页
            </Text>{" "}
            <Text size="xs" c="red" span>
              {error}
            </Text>
          </Paper>
        ))}
      </ScrollArea>
      <Text size="xs" c="dimmed" mt="xs">
        {t.operation === "migrate"
          ? "迁移仅处理现有图片；完成后可从“已下载”验证并补全。"
          : "继续 / 重试会校验所有已完成文件，只补全缺失或损坏页面。"}
      </Text>
    </>
  );
}
