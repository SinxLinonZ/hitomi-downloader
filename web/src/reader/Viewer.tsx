import { useCallback, useEffect, useRef, useState } from "react";
import { ActionIcon, Loader, Slider, Text } from "@mantine/core";
import { IconArrowLeft, IconBook2, IconLayoutRows, IconSwitchHorizontal } from "@tabler/icons-react";
import { api } from "../api";
import { loadPrefs, loadProgress, Prefs, saveProgress, savePrefs } from "./progress";
import type { ReaderWork } from "./types";

const pageUrl = (id: number, n: number) => `/api/v1/reader/works/${id}/pages/${n}`;
const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function Viewer({ id, onClose }: { id: number; onClose: () => void }) {
  const [work, setWork] = useState<ReaderWork>();
  const [error, setError] = useState("");
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs);
  const [page, setPage] = useState(1);
  const [bar, setBar] = useState(true);
  const initial = useRef(Math.max(1, loadProgress()[String(id)]?.page || 1));

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const load = () =>
      api<ReaderWork>(`/reader/works/${id}`, undefined, undefined, controller.signal)
        .then((w) => {
          setWork(w);
          setPage(Math.min(initial.current, Math.max(1, w.pageList.length)));
        })
        .catch((e) => {
          if (controller.signal.aborted) return;
          // 起動直後でまだ走査中の場合は、見つかるまで待つ。
          if (/扫描中/.test(errorText(e))) timer = setTimeout(load, 1000);
          else setError(errorText(e));
        });
    void load();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [id]);

  // 閲覧位置を保存する（頻繁な書き込みを避けて少し待つ）。
  useEffect(() => {
    if (!work) return;
    const timer = setTimeout(() => saveProgress(id, page, work.pageList.length), 400);
    return () => clearTimeout(timer);
  }, [id, page, work]);

  const update = (next: Partial<Prefs>) =>
    setPrefs((prev) => {
      const merged = { ...prev, ...next };
      savePrefs(merged);
      return merged;
    });
  const title = work ? work.japaneseTitle?.trim() || work.title : "";
  const total = work?.pageList.length ?? 0;

  return (
    <div className="viewer" role="dialog" aria-label="阅览">
      {!work && !error && (
        <div className="v-center">
          <Loader />
        </div>
      )}
      {error && (
        <div className="v-center">
          <Text c="red" ta="center" px="md">
            {error}
          </Text>
        </div>
      )}
      {work &&
        (prefs.mode === "scroll" ? (
          <ScrollMode id={id} work={work} start={initial.current} page={page} onPage={setPage} onTap={() => setBar((b) => !b)} onScrollAway={() => setBar(false)} />
        ) : (
          <PagedMode id={id} work={work} page={page} rtl={prefs.rtl} onPage={setPage} onTap={() => setBar((b) => !b)} />
        ))}
      <div className={`v-bar v-top${bar ? "" : " v-hidden"}`}>
        <ActionIcon variant="subtle" color="gray" size={42} aria-label="返回列表" onClick={onClose}>
          <IconArrowLeft size={22} />
        </ActionIcon>
        <div className="v-title">{title}</div>
        {prefs.mode === "paged" && (
          <ActionIcon variant="subtle" color={prefs.rtl ? "teal" : "gray"} size={42} aria-label={prefs.rtl ? "右开（点左侧下一页）" : "左开（点右侧下一页）"} onClick={() => update({ rtl: !prefs.rtl })}>
            <IconSwitchHorizontal size={20} />
          </ActionIcon>
        )}
        <ActionIcon variant="subtle" color="gray" size={42} aria-label={prefs.mode === "scroll" ? "切换为翻页模式" : "切换为滚动模式"} onClick={() => update({ mode: prefs.mode === "scroll" ? "paged" : "scroll" })}>
          {prefs.mode === "scroll" ? <IconBook2 size={20} /> : <IconLayoutRows size={20} />}
        </ActionIcon>
      </div>
      {work && total > 0 && (
        <div className={`v-bar v-bottom${bar ? "" : " v-hidden"}`}>
          <Text size="sm" w={72} ta="center">
            {page} / {total}
          </Text>
          <Slider
            style={{ flex: 1 }}
            aria-label="跳转到页"
            min={1}
            max={total}
            step={1}
            value={page}
            onChange={setPage}
            label={null}
            thumbSize={22}
          />
        </div>
      )}
    </div>
  );
}

function PageImage({ id, n, ratio, active }: { id: number; n: number; ratio?: number; active: boolean }) {
  const [failed, setFailed] = useState(false);
  const [natural, setNatural] = useState<number>();
  const r = ratio || natural;
  return (
    <div className="pg" data-page={n} style={r ? { aspectRatio: String(r) } : { minHeight: "60vh" }}>
      {active &&
        (failed ? (
          <div className="pg-missing">第 {n} 页缺失或格式不受支持</div>
        ) : (
          <img
            src={pageUrl(id, n)}
            alt={`第 ${n} 页`}
            decoding="async"
            onLoad={(e) => setNatural(e.currentTarget.naturalWidth / e.currentTarget.naturalHeight)}
            onError={() => setFailed(true)}
          />
        ))}
    </div>
  );
}

// 縦スクロール：全ページを並べ、現在位置の前後だけ画像を読み込む（枚数が多い作品でもメモリを抑える）。
function ScrollMode({ id, work, start, page, onPage, onTap, onScrollAway }: { id: number; work: ReaderWork; start: number; page: number; onPage: (n: number) => void; onTap: () => void; onScrollAway: () => void }) {
  const box = useRef<HTMLDivElement>(null);
  const lastTop = useRef(0);
  const frame = useRef(0);
  const target = useRef<number | null>(null);

  const scrollToPage = useCallback((n: number) => {
    const el = box.current?.querySelector<HTMLElement>(`[data-page="${n}"]`);
    if (el && box.current) box.current.scrollTop = el.offsetTop;
  }, []);
  // 初期位置の復元。
  useEffect(() => {
    scrollToPage(start);
    lastTop.current = box.current?.scrollTop ?? 0;
  }, [scrollToPage, start]);
  // スライダー等による外部からのページ変更。スクロール由来の変更では動かさない。
  useEffect(() => {
    if (target.current === page) {
      target.current = null;
      return;
    }
    scrollToPage(page);
  }, [page, scrollToPage]);

  const onScroll = () => {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(() => {
      const el = box.current;
      if (!el) return;
      if (Math.abs(el.scrollTop - lastTop.current) > 30) {
        onScrollAway();
        lastTop.current = el.scrollTop;
      }
      const line = el.scrollTop + el.clientHeight * 0.4;
      const pages = el.querySelectorAll<HTMLElement>("[data-page]");
      let current = 1;
      for (const p of pages) {
        if (p.offsetTop <= line) current = Number(p.dataset.page);
        else break;
      }
      if (current !== page) {
        target.current = current;
        onPage(current);
      }
    });
  };

  return (
    <div className="v-scroll" ref={box} onScroll={onScroll} onClick={onTap}>
      {work.pageList.map((p, i) => (
        <PageImage key={i} id={id} n={i + 1} ratio={p.w && p.h ? p.w / p.h : undefined} active={Math.abs(i + 1 - page) <= 4} />
      ))}
      <div className="v-end">— 完 —</div>
    </div>
  );
}

// 単ページ：左右のタップとスワイプでめくる。
function PagedMode({ id, work, page, rtl, onPage, onTap }: { id: number; work: ReaderWork; page: number; rtl: boolean; onPage: (n: number) => void; onTap: () => void }) {
  const total = work.pageList.length;
  const [failed, setFailed] = useState<number>();
  const touch = useRef<{ x: number; y: number } | null>(null);
  const go = useCallback((delta: number) => onPage(Math.min(total, Math.max(1, page + delta))), [onPage, page, total]);
  // 次の 2 ページを先読みする。
  useEffect(() => {
    for (let d = 1; d <= 2; d++) {
      if (page + d <= total) new Image().src = pageUrl(id, page + d);
    }
  }, [id, page, total]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "ArrowRight") go(rtl ? -1 : 1);
      else if (e.key === "ArrowLeft") go(rtl ? 1 : -1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [go, rtl]);
  const tap = (e: React.MouseEvent) => {
    const x = e.clientX / window.innerWidth;
    if (x < 0.3) go(rtl ? 1 : -1);
    else if (x > 0.7) go(rtl ? -1 : 1);
    else onTap();
  };
  return (
    <div
      className="v-paged"
      onClick={tap}
      onTouchStart={(e) => (touch.current = { x: e.touches[0].clientX, y: e.touches[0].clientY })}
      onTouchEnd={(e) => {
        const t = touch.current;
        touch.current = null;
        if (!t) return;
        const dx = e.changedTouches[0].clientX - t.x;
        const dy = e.changedTouches[0].clientY - t.y;
        if (Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.5) {
          // 右開きでは右へのスワイプが「次のページ」。
          go((dx < 0) === !rtl ? 1 : -1);
          e.preventDefault();
        }
      }}
    >
      {failed === page ? (
        <div className="pg-missing">第 {page} 页缺失或格式不受支持</div>
      ) : (
        <img key={page} src={pageUrl(id, page)} alt={`第 ${page} 页`} onError={() => setFailed(page)} />
      )}
    </div>
  );
}
