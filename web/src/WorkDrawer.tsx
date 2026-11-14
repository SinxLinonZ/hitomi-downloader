import { useEffect, useState } from "react";
import {
  Badge,
  Button,
  Drawer,
  Group,
  Loader,
  Paper,
  Text,
  Title,
  Tooltip,
} from "@mantine/core";
import { IconArrowLeft, IconDownload } from "@tabler/icons-react";
import { api, Gallery } from "./api";
import { Cover, errorText, titles } from "./common";
import { statusHint, useWorkStatus } from "./downloadStatus";
import { addDownload, GalleryCard } from "./GalleryCard";

// 詳細を開く対象。ローカルの作品は表紙とディレクトリを指定できる。
export type Opened = { gallery: Gallery; coverSrc?: string; directory?: string };

export function WorkDrawer({
  opened,
  onClose,
}: {
  opened?: Opened;
  onClose: () => void;
}) {
  // 関連作品をたどれるよう、開いた詳細を履歴として積む。
  const [extra, setExtra] = useState<Gallery[]>([]);
  useEffect(() => setExtra([]), [opened]);
  const current = extra[extra.length - 1] ?? opened?.gallery;
  const root = extra.length === 0;
  return (
    <Drawer
      opened={!!opened}
      onClose={onClose}
      title="作品详情"
      position="right"
      size="lg"
      padding="md"
    >
      {current && (
        <>
          {!root && (
            <Button
              variant="subtle"
              color="gray"
              mb="xs"
              leftSection={<IconArrowLeft size={14} />}
              onClick={() => setExtra((t) => t.slice(0, -1))}
            >
              返回上一个作品
            </Button>
          )}
          <WorkDetail
            key={current.id}
            g={current}
            coverSrc={root ? opened?.coverSrc : undefined}
            directory={root ? opened?.directory : undefined}
            onOpen={(g) => setExtra((t) => [...t, g])}
          />
        </>
      )}
    </Drawer>
  );
}

type RelatedItem = { id: number; gallery?: Gallery; error?: string };

// Hitomi の作品ページと同様に、関連作品を詳細の下に並べる。
function Related({
  id,
  onOpen,
}: {
  id: number | string;
  onOpen: (g: Gallery) => void;
}) {
  const [items, setItems] = useState<RelatedItem[]>();
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    api<{ items: RelatedItem[] }>(
      `/galleries/${id}/related`,
      undefined,
      undefined,
      controller.signal,
    )
      .then((r) => setItems(r.items))
      .catch((e) => {
        if (!controller.signal.aborted) setError(errorText(e));
      });
    return () => controller.abort();
  }, [id]);
  return (
    <section aria-label="相关作品" style={{ marginTop: 16 }}>
      <Group gap={6} mb="xs">
        <Text fw={600} size="sm">
          相关作品{items ? `（${items.length}）` : ""}
        </Text>
        {!items && !error && <Loader size={12} />}
      </Group>
      {error && (
        <Text size="xs" c="red">
          {error}
        </Text>
      )}
      {items && items.length === 0 && (
        <Text size="xs" c="dimmed">
          没有相关作品。
        </Text>
      )}
      {items && items.length > 0 && (
        <div className="gallery-grid related-grid">
          {items.map((item) =>
            item.gallery ? (
              <GalleryCard
                key={item.id}
                id={item.id}
                g={item.gallery}
                onOpen={onOpen}
              />
            ) : (
              <Paper withBorder key={item.id} className="g-card">
                <div className="g-error">
                  <Text size="xs" c="dimmed">
                    #{item.id}
                  </Text>
                  <Text size="xs" c="red">
                    {item.error}
                  </Text>
                </div>
              </Paper>
            ),
          )}
        </div>
      )}
    </section>
  );
}

function WorkDetail({
  g,
  coverSrc,
  directory,
  onOpen,
}: {
  g: Gallery;
  coverSrc?: string;
  directory?: string;
  onOpen: (g: Gallery) => void;
}) {
  const { main, sub } = titles(g.title, g.japanese_title);
  const artists = g.artists?.map((a) => a.artist).join("、");
  // 本地の作品（directory あり）には下载按钮がないので、状態の問い合わせも不要。
  const status = useWorkStatus(g.id, !directory);
  const hint = statusHint(status);
  return (
    <>
      <div className="detail-head">
        <Cover src={coverSrc || `/api/v1/covers/${g.id}`} alt={`${main} 封面`} />
        <div style={{ minWidth: 0, flex: 1 }}>
          <Title order={4} lh={1.35}>
            {main}
          </Title>
          {sub && (
            <Text
              size="xs"
              c="dimmed"
              mt={2}
              className="work-title-secondary"
              style={{ whiteSpace: "normal" }}
            >
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
          {directory && (
            <Text size="xs" c="dimmed" mt={6} className="path">
              {directory}
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
      {!directory && (
        <Tooltip label={hint} disabled={!hint}>
          <div style={{ marginTop: 12 }}>
            <Button
              fullWidth
              leftSection={<IconDownload size={15} />}
              disabled={!!hint}
              onClick={() => addDownload(g.id)}
            >
              {hint ?? "加入下载队列"}
            </Button>
          </div>
        </Tooltip>
      )}
      <Related id={g.id} onOpen={onOpen} />
    </>
  );
}
