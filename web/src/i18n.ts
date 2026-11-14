import { en } from "./locales/en";
import { ja } from "./locales/ja";

// 文言は簡体字中国語の原文をキーにする。翻訳が無ければ原文を表示する。
export type Locale = "zh" | "ja" | "en";
export type LocaleSetting = Locale | "auto";
export const LOCALES: { value: Locale; label: string }[] = [
  { value: "zh", label: "简体中文" },
  { value: "ja", label: "日本語" },
  { value: "en", label: "English" },
];

const STORAGE_KEY = "hitomi.lang";

const normalize = (s: string | null | undefined): Locale | undefined => {
  const v = (s || "").toLowerCase();
  if (v.startsWith("zh")) return "zh";
  if (v.startsWith("ja")) return "ja";
  if (v.startsWith("en")) return "en";
  return undefined;
};

// 保存した選択 ＞ ブラウザの言語 ＞ 英語。
export function savedSetting(): LocaleSetting {
  try {
    return normalize(localStorage.getItem(STORAGE_KEY)) ?? "auto";
  } catch {
    return "auto";
  }
}
function detect(): Locale {
  const saved = savedSetting();
  if (saved !== "auto") return saved;
  for (const l of navigator.languages?.length ? navigator.languages : [navigator.language]) {
    const n = normalize(l);
    if (n) return n;
  }
  return "en";
}

export const locale: Locale = detect();
document.documentElement.lang = locale === "zh" ? "zh-CN" : locale;

const catalog: Record<string, string> | undefined = locale === "ja" ? ja : locale === "en" ? en : undefined;

// t("共 {n} 个作品", { n: 3 }) のように、{名前} を値で置き換える。
export function t(source: string, params?: Record<string, string | number>): string {
  let text = catalog?.[source] ?? source;
  if (params) for (const [k, v] of Object.entries(params)) text = text.replaceAll(`{${k}}`, String(v));
  return text;
}

// 言語を保存して読み込み直す。画面の文言はモジュール読み込み時に決まるため。
export function setLocale(setting: LocaleSetting) {
  try {
    if (setting === "auto") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, setting);
  } catch {
    /* 保存できない環境では、次回も自動判定になる。 */
  }
  location.reload();
}

// 日付などの Intl 用の言語タグ。
export const intlLocale = locale === "zh" ? "zh-CN" : locale;
