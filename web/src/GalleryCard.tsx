import { ActionIcon, Group, Paper, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconArrowDown, IconCheck, IconInfoCircle } from "@tabler/icons-react";
import { api, Gallery } from "./api";
import { Cover, report, titles } from "./common";
import { t } from "./i18n";
import { statusHint, useWorkStatus } from "./downloadStatus";

export async function addDownload(id: number | string) {
  try {
    await api("/downloads", { id: Number(id) });
    notifications.show({
      title: t("已加入下载队列"),
      message: t("作品 #{id} · 优先获取源文件", { id }),
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
        alt={t("{title} 封面", { title: main })}
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
            {g.language || t("未知语言")} · {t("{n} 页", { n: g.files.length })}
          </span>
          <Group gap={0} wrap="nowrap">
            <Tooltip label={t("查看详情")}>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="sm"
                aria-label={t("查看详情")}
                onClick={() => onOpen(g)}
              >
                <IconInfoCircle size={15} />
              </ActionIcon>
            </Tooltip>
            {/* 置灰した按钮は hover を拾えないため、Tooltip の対象は外側の span にする。 */}
            <Tooltip label={hint ?? t("加入下载队列")}>
              <span>
                <ActionIcon
                  variant="light"
                  size="sm"
                  aria-label={hint ? `${hint} #${id}` : t("下载 #{id}", { id })}
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

