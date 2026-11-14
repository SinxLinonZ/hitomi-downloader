import { ActionIcon, Group, Paper, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconArrowDown, IconCheck, IconInfoCircle } from "@tabler/icons-react";
import { api, Gallery } from "./api";
import { Cover, report, titles } from "./common";
import { statusHint, useWorkStatus } from "./downloadStatus";

export async function addDownload(id: number | string) {
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


export function GalleryCard({
  id,
  g,
  coverKey = 0,
  onOpen,
}: {
  id: number | string;
  g: Gallery;
  coverKey?: number;
  onOpen: (g: Gallery) => void;
}) {
  const { main, sub } = titles(g.title, g.japanese_title);
  const status = useWorkStatus(id);
  const hint = statusHint(status);
  return (
    <Paper withBorder className="g-card">
      <Cover
        key={`${id}-${coverKey}`}
        src={`/api/v1/covers/${id}?v=${coverKey}`}
        alt={`${main} 封面`}
        onClick={() => onOpen(g)}
      />
      <span className="g-id">#{id}</span>
      <div className="g-body">
        <div
          className="gallery-title"
          title={sub ? `${main}\n${sub}` : main}
          onClick={() => onOpen(g)}
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
                onClick={() => onOpen(g)}
              >
                <IconInfoCircle size={15} />
              </ActionIcon>
            </Tooltip>
            {/* 置灰した按钮は hover を拾えないため、Tooltip の対象は外側の span にする。 */}
            <Tooltip label={hint ?? "加入下载队列"}>
              <span>
                <ActionIcon
                  variant="light"
                  size="sm"
                  aria-label={hint ? `${hint} #${id}` : `下载 #${id}`}
                  disabled={!!hint}
                  onClick={() => addDownload(id)}
                >
                  {status === "downloaded" ? (
                    <IconCheck size={15} />
                  ) : (
                    <IconArrowDown size={15} />
                  )}
                </ActionIcon>
              </span>
            </Tooltip>
          </Group>
        </div>
      </div>
    </Paper>
  );
}

