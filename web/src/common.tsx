import { Image, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconPhotoOff } from "@tabler/icons-react";
import { Fragment, ReactNode } from "react";
import type { Task } from "./api";
import { t } from "./i18n";

export function report(e: unknown) {
  notifications.show({
    color: "red",
    title: t("操作未完成"),
    message: e instanceof Error ? e.message : String(e),
    autoClose: false,
  });
}

// 文言の {名前} を React ノードに置き換える（<code> など、文中に要素を入れたいとき用）。
export function tx(source: string, nodes: Record<string, ReactNode>): ReactNode {
  return t(source)
    .split(/(\{\w+\})/)
    .map((part, i) => {
      const key = /^\{(\w+)\}$/.exec(part)?.[1];
      return <Fragment key={i}>{key && key in nodes ? nodes[key] : part}</Fragment>;
    });
}

export function errorText(e: unknown) {
  return e instanceof Error ? e.message : String(e);
}

export const basename = (path: string) =>
  path.split(/[\\/]/).filter(Boolean).pop() || "";

// 原文タイトルを主表示にし、訳題が異なる場合だけ副題として返す。
export function titles(title: string | undefined, japanese?: string) {
  const jp = japanese?.trim() || "";
  const t = title?.trim() || "";
  return { main: jp || t, sub: jp && t && jp !== t ? t : "" };
}

// タイトル未取得の移行タスクは、ディレクトリ名で識別できるようにする。
export const taskTitle = (task: Task) =>
  task.title || basename(task.directory) || t("作品 #{id}", { id: task.id });

export const operationLabel = (op: string) =>
  op === "repair"
    ? t("验证并补全")
    : op === "migrate"
      ? t("迁移")
      : op === "update"
        ? t("应用上游更新")
        : t("下载");

// 一覧の表示切り替えで使う、状態のグルーピング。
export const isActive = (task: Task) => ["running", "queued"].includes(task.state);
export const isResumable = (task: Task) =>
  ["paused", "failed", "cancelled"].includes(task.state);

const FALLBACK =
  "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='90' height='124'%3E%3Crect fill='%23222832' width='90' height='124'/%3E%3C/svg%3E";

// Mantine Image を使い、読み込み失敗時は無地のプレースホルダーに切り替える。
export function Cover({
  src,
  alt,
  className,
  onClick,
}: {
  src: string;
  alt: string;
  className?: string;
  onClick?: () => void;
}) {
  return (
    <Image
      onClick={onClick}
      className={`cover ${className || ""}`}
      src={src}
      alt={alt}
      loading="lazy"
      fallbackSrc={FALLBACK}
    />
  );
}

export function Empty({ text }: { text: string }) {
  return (
    <div className="empty">
      <IconPhotoOff size={22} />
      <Text c="dimmed" size="sm">
        {text}
      </Text>
    </div>
  );
}
