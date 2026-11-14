import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { MantineProvider, createTheme } from "@mantine/core";
import "@mantine/core/styles.css";
import "./reader.css";
import { ReaderList } from "./ReaderList";
import { Viewer } from "./Viewer";

const theme = createTheme({
  primaryColor: "teal",
  fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
});

// 画面の状態を URL の hash で表す：#/w/123＝作品を閲覧、#/filter＝筛选と排序の抽屉。
type Route = { work?: number; filter: boolean };
const readRoute = (): Route => {
  const m = location.hash.match(/^#\/w\/(\d+)$/);
  return { work: m ? Number(m[1]) : undefined, filter: location.hash === "#/filter" };
};
// 履歴に積んだ記録がこの文書で作ったものかを見分ける印。
// 再読み込み後は直前の記録が破棄された文書のものなので、back() してはいけない。
const DOC = Math.random().toString(36).slice(2);
const ownEntry = () => (history.state as { doc?: string } | null)?.doc === DOC;
const sameRoute = (a: Route, b: Route) => a.work === b.work && a.filter === b.filter;

function App() {
  const [route, setRoute] = useState(readRoute);
  const [progressRevision, setProgressRevision] = useState(0);
  const current = useRef(route);
  const sync = () => {
    const next = readRoute();
    const prev = current.current;
    if (sameRoute(prev, next)) return;
    current.current = next;
    // 閲覧から戻ったら、一覧の「读到 N/M」を最新にする。
    if (prev.work !== undefined && next.work === undefined) setProgressRevision((v) => v + 1);
    setRoute(next);
  };
  useEffect(() => {
    window.addEventListener("popstate", sync);
    window.addEventListener("hashchange", sync);
    return () => {
      window.removeEventListener("popstate", sync);
      window.removeEventListener("hashchange", sync);
    };
  }, []);
  // 閲覧中は背面の一覧をスクロールさせない（一覧は表示したまま残し、戻ると位置が保たれる）。
  useEffect(() => {
    document.body.style.overflow = route.work ? "hidden" : "";
  }, [route.work]);

  // 開くときは履歴を 1 つ積む。pushState は hashchange を起こさないので自分で同期する。
  const open = (hash: string) => {
    history.pushState({ doc: DOC }, "", hash);
    sync();
  };
  // 閉じるときは、自分で積んだ記録なら 1 つ戻る。そうでなければ（再読み込み後や直接リンク）
  // ページを離れずに、この記録を一覧へ置き換える。
  const close = () => {
    if (ownEntry()) history.back();
    else {
      history.replaceState(null, "", location.pathname + location.search);
      sync();
    }
  };
  return (
    <>
      <ReaderList
        onOpen={(id) => open(`#/w/${id}`)}
        progressRevision={progressRevision}
        filterOpen={route.filter && route.work === undefined}
        onFilterOpen={() => open("#/filter")}
        onFilterClose={close}
      />
      {route.work !== undefined && <Viewer key={route.work} id={route.work} onClose={close} />}
    </>
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <MantineProvider theme={theme} forceColorScheme="dark">
      <App />
    </MantineProvider>
  </React.StrictMode>,
);
