import React, { useEffect, useState } from "react";
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

// #/w/123 で作品を開く。ブラウザの「戻る」で一覧に戻れる。
const workFromHash = () => {
  const m = location.hash.match(/^#\/w\/(\d+)$/);
  return m ? Number(m[1]) : undefined;
};

function App() {
  const [work, setWork] = useState(workFromHash);
  const [progressRevision, setProgressRevision] = useState(0);
  useEffect(() => {
    const onHash = () => {
      setWork(workFromHash());
      setProgressRevision((v) => v + 1);
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  // 閲覧中は背面の一覧をスクロールさせない（一覧は表示したまま残し、戻ると位置が保たれる）。
  useEffect(() => {
    document.body.style.overflow = work ? "hidden" : "";
  }, [work]);
  return (
    <>
      <ReaderList onOpen={(id) => (location.hash = `#/w/${id}`)} progressRevision={progressRevision} />
      {work !== undefined && (
        <Viewer
          key={work}
          id={work}
          onClose={() => {
            // 一覧から開いた場合は履歴を戻し、直接リンクで開いた場合は一覧へ置き換える。
            if (history.length > 1) history.back();
            else location.hash = "";
          }}
        />
      )}
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
