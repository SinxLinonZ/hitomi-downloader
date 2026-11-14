import {
  createContext,
  ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { api, Task } from "./api";
import { isActive } from "./common";

// 作品がすでに本地库にあるか／キューに入っているかを、画面全体で共有する。
export type WorkStatus = "downloaded" | "active" | undefined;

type Value = {
  status: (id: number | string) => WorkStatus;
  watch: (id: number | string) => void;
};
const Context = createContext<Value>({ status: () => undefined, watch: () => {} });

export function DownloadStatusProvider({
  tasks,
  children,
}: {
  tasks: Task[];
  children: ReactNode;
}) {
  // サーバーに問い合わせて「ダウンロード済み」と確認できた ID。
  const [known, setKnown] = useState<Set<number>>(new Set());
  const asked = useRef(new Set<number>());
  const pending = useRef(new Set<number>());
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const watch = useCallback((raw: number | string) => {
    const id = Number(raw);
    if (!id || asked.current.has(id)) return;
    asked.current.add(id);
    pending.current.add(id);
    // 同じ画面のカードをまとめて 1 回の問い合わせにする。
    clearTimeout(timer.current);
    timer.current = setTimeout(async () => {
      const ids = Array.from(pending.current);
      pending.current.clear();
      try {
        const params = new URLSearchParams();
        ids.forEach((i) => params.append("id", String(i)));
        const r = await api<{ ids: number[] }>(`/library/downloaded?${params}`);
        if (r.ids.length)
          setKnown((prev) => new Set([...prev, ...r.ids]));
      } catch {
        // 判定できなかった ID は、次に表示されたときに再確認する。
        ids.forEach((i) => asked.current.delete(i));
      }
    }, 40);
  }, []);
  useEffect(() => () => clearTimeout(timer.current), []);

  const byId = useMemo(() => {
    const map = new Map<number, Task>();
    for (const t of tasks) map.set(t.id, t);
    return map;
  }, [tasks]);

  const value = useMemo<Value>(
    () => ({
      watch,
      status: (raw) => {
        const id = Number(raw);
        const task = byId.get(id);
        // 完了済みのタスクは、ダウンロード先に作品があることを意味する。
        if (known.has(id) || task?.state === "completed") return "downloaded";
        if (task && isActive(task)) return "active";
        return undefined;
      },
    }),
    [watch, byId, known],
  );
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

// 表示中の作品の状態を返し、初回だけサーバーに問い合わせる。
export function useWorkStatus(
  id: number | string,
  enabled = true,
): WorkStatus {
  const { status, watch } = useContext(Context);
  useEffect(() => {
    if (enabled) watch(id);
  }, [id, enabled, watch]);
  return status(id);
}

export const statusHint = (s: WorkStatus) =>
  s === "downloaded"
    ? "已下载"
    : s === "active"
      ? "已在下载队列中"
      : undefined;
