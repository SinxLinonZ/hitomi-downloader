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
