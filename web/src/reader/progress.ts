// 閲覧位置と表示設定はこの端末のブラウザにだけ保存する。
const PROGRESS_KEY = "hitomi.reader.progress";
const PREFS_KEY = "hitomi.reader.prefs";

export type Progress = { page: number; total: number; at: number };
export type Prefs = { mode: "scroll" | "paged"; rtl: boolean };

function read<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? { ...fallback, ...JSON.parse(raw) } : fallback;
  } catch {
    return fallback;
  }
}
function write(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* 保存できない環境でも閲覧は続けられる。 */
  }
}

export const loadProgress = (): Record<string, Progress> =>
  read<Record<string, Progress>>(PROGRESS_KEY, {});
export function saveProgress(id: number, page: number, total: number) {
  const all = loadProgress();
  all[String(id)] = { page, total, at: Date.now() };
  // 古い順に捨てて、保存量が増え続けないようにする。
  const keys = Object.keys(all);
  if (keys.length > 500) {
    keys
      .sort((a, b) => all[a].at - all[b].at)
      .slice(0, keys.length - 500)
      .forEach((k) => delete all[k]);
  }
  write(PROGRESS_KEY, all);
}
export const loadPrefs = (): Prefs =>
  read<Prefs>(PREFS_KEY, { mode: "scroll", rtl: false });
export const savePrefs = (prefs: Prefs) => write(PREFS_KEY, prefs);

// 一覧の並び順（この端末にだけ保存）。既定は新しくダウンロードした順。
export type SortKey = "created" | "created-asc" | "updated";
const SORT_KEY = "hitomi.reader.sort";
export const SORT_OPTIONS: { value: SortKey; label: string }[] = [
  { value: "created", label: "最近下载" },
  { value: "created-asc", label: "最早下载" },
  { value: "updated", label: "最近更新" },
];
export function loadSort(): SortKey {
  try {
    const v = localStorage.getItem(SORT_KEY);
    if (SORT_OPTIONS.some((o) => o.value === v)) return v as SortKey;
  } catch {
    /* 読めない環境では既定値。 */
  }
  return "created";
}
export function saveSort(sort: SortKey) {
  try {
    localStorage.setItem(SORT_KEY, sort);
  } catch {
    /* 保存できなくても動作は変わらない。 */
  }
}
