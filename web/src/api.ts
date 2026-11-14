import { t } from "./i18n";
// サーバーのエラー応答。code でエラーの種類を判定できる（message は表示用の文言）。
export class ApiError extends Error {
  constructor(
    message: string,
    public code: string,
    public status: number,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  body?: unknown,
  method?: string,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    method: method || (body ? "POST" : "GET"),
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
    signal,
  });
  if (!response.ok) {
    const text = await response.text();
    let message = text;
    let code = "";
    try {
      const error = JSON.parse(text).error;
      message = error.message;
      code = error.code || "";
    } catch {
      /* テキスト応答も表示する。 */
    }
    throw new ApiError(message || `HTTP ${response.status}`, code, response.status);
  }
  return response.json() as Promise<T>;
}
export type Gallery = {
  id: number | string;
  title: string;
  japanese_title?: string;
  language?: string;
  type: string;
  files: { name: string; width: number; height: number }[];
  artists?: { artist: string }[];
  tags?: { tag: string; female?: number; male?: number }[];
};
export type Task = {
  warning?: string;
  id: number;
  title: string;
  state: string;
  operation: string;
  directory: string;
  done: number;
  total: number;
  errors: Record<string, string>;
  error?: string;
};
export type TaskBatchResult = {
  affected: number;
  skipped: number;
  errors: Record<string, string>;
};
export type Entry = {
  tags: { name: string; namespace: string }[];
  updatedAt?: string;
  createdAt?: string;
  legacy: boolean;
  artists: string[];
  language: string;
  type: string;
  japaneseTitle?: string;
  missing: number;
  formats: string[];
  fallbacks: number;
  upstream?: "changed" | "deleted";
  upstreamNote?: string;
  id: number;
  title: string;
  directory: string;
  pages: number;
};
export type LibraryPage = {
  tags: { value: string; count: number }[];
  entries: Entry[];
  total: number;
  legacyTotal: number;
  page: number;
  pageSize: number;
  scan: {
    state: string;
    root: string;
    visited: number;
    found: number;
    current: string;
    warnings: string[];
    warningCount: number;
    error?: string;
  };
};
export type Search = {
  session: string;
  page: number;
  total: number;
  items: { id: number; gallery?: Gallery; error?: string }[];
};
export const stateLabels: Record<string, string> = {
  queued: t("排队中"),
  running: t("处理中"),
  paused: t("已暂停"),
  cancelled: t("已取消"),
  failed: t("失败"),
  completed: t("已完成"),
};
export type Settings = {
  downloadDir: string;
  language?: string;
  effectiveLanguage?: string;
  taskConcurrency?: number;
  pageConcurrency?: number;
};
