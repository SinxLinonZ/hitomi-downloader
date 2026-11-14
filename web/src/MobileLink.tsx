import { useEffect, useState } from "react";
import { Button, Group, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconCopy, IconExternalLink } from "@tabler/icons-react";
import qrcode from "qrcode-generator";
import { api } from "./api";

type Info = { lan: boolean; urls: string[]; localUrl: string };

function qrSvg(text: string) {
  const qr = qrcode(0, "M");
  qr.addData(text);
  qr.make();
  return qr.createSvgTag({ cellSize: 4, margin: 2, scalable: true });
}

// 手機で閲覧ページ（/reader）を開くためのリンクと QR コード。閲覧専用の口令を含む。
export function MobileLink() {
  const [info, setInfo] = useState<Info>();
  useEffect(() => {
    api<Info>("/mobile-link").then(setInfo).catch(() => undefined);
  }, []);
  if (!info) return null;
  const copy = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url);
      notifications.show({ color: "teal", message: "已复制链接" });
    } catch {
      notifications.show({ color: "orange", message: "无法自动复制，请手动选择链接" });
    }
  };
  return (
    <div>
      <Text size="sm" fw={600}>
        手机阅读（只读）
      </Text>
      {!info.lan ? (
        <Text size="xs" c="dimmed" mt={4}>
          目前只监听本机。以 <code>--lan</code> 启动后，同一局域网的手机即可打开阅读页（只能浏览和阅读，不能下载或修改设置）。
        </Text>
      ) : info.urls.length === 0 ? (
        <Text size="xs" c="dimmed" mt={4}>
          没有找到本机的局域网地址，请确认已连接到局域网。
        </Text>
      ) : (
        <>
          <Text size="xs" c="dimmed" mt={4}>
            用手机扫描二维码，或打开下面的链接。链接含有阅读专用口令，请只在自己的设备上使用。
          </Text>
          {info.urls.map((url) => (
            <div key={url} style={{ marginTop: 8 }}>
              <div
                aria-label="手机阅读二维码"
                role="img"
                style={{ width: 150, background: "#fff", padding: 6, borderRadius: 6 }}
                dangerouslySetInnerHTML={{ __html: qrSvg(url) }}
              />
              <Group gap={6} mt={6} wrap="nowrap">
                <Text size="xs" c="dimmed" truncate style={{ flex: 1 }} title={url}>
                  {url}
                </Text>
                <Button size="compact-xs" variant="light" leftSection={<IconCopy size={12} />} onClick={() => copy(url)}>
                  复制
                </Button>
              </Group>
            </div>
          ))}
        </>
      )}
      <Button component="a" href={info.localUrl} target="_blank" rel="noreferrer" mt={8} size="compact-sm" variant="default" leftSection={<IconExternalLink size={14} />}>
        在本机打开阅读页
      </Button>
    </div>
  );
}
