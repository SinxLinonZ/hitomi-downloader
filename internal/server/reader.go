package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hitomi-downloader/internal/download"
	"hitomi-downloader/web"
)

const readerCookie = "hitomi_reader"

// 閲覧専用トークンを読み込む。なければ生成して保存する（再起動後も同じリンクで使える）。
// 閲覧用の API だけを許可する資格情報で、ダウンロードや設定の操作はできない。
func loadReaderToken(state string) string {
	path := filepath.Join(state, "reader-token")
	if b, e := os.ReadFile(path); e == nil {
		if token := strings.TrimSpace(string(b)); len(token) >= 32 {
			return token
		}
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	token := hex.EncodeToString(b)
	_ = os.MkdirAll(state, 0700)
	_ = os.WriteFile(path, []byte(token+"\n"), 0600) // 保存できなくても、この起動中は有効
	return token
}

func isLoopbackAddr(addr string) bool {
	host, _, e := net.SplitHostPort(addr)
	if e != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Host ヘッダーを検証する（DNS rebinding 対策）。LAN モードでは IP リテラルと .local 名も許可する。
func (s *Server) hostAllowed(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	if !s.LAN {
		return false
	}
	return net.ParseIP(host) != nil || strings.HasSuffix(host, ".local")
}

// LAN 上の他の端末からは、閲覧ページとその API・静的ファイルだけ使える。
func readerStaticPath(path string) bool {
	return path == "/reader" || path == "/reader/" || strings.HasPrefix(path, "/assets/") || path == "/favicon.ico"
}

// privateIPv4s は、手機から到達できる本機の私有 IPv4 アドレスを返す。
func privateIPv4s() []string {
	addrs, e := net.InterfaceAddrs()
	if e != nil {
		return nil
	}
	out := []string{}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if ip := n.IP.To4(); ip != nil && ip.IsPrivate() {
				out = append(out, ip.String())
			}
		}
	}
	return out
}

func failStatus(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": status == http.StatusServiceUnavailable}})
}

func readerError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, download.ErrScanning):
		failStatus(w, http.StatusServiceUnavailable, "SCANNING", e.Error())
	case errors.Is(e, os.ErrNotExist):
		failStatus(w, http.StatusNotFound, "NOT_FOUND", "找不到该作品或页面")
	default:
		fail(w, e)
	}
}

var imageTypes = map[string]string{".webp": "image/webp", ".avif": "image/avif", ".jxl": "image/jxl", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif"}

func serveImage(w http.ResponseWriter, r *http.Request, path string) {
	if t := imageTypes[strings.ToLower(filepath.Ext(path))]; t != "" {
		w.Header().Set("Content-Type", t)
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}

func (s *Server) registerReader(mux *http.ServeMux) {
	page := func(w http.ResponseWriter, r *http.Request) {
		b, e := web.Assets.ReadFile("dist/reader.html")
		if e != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
	mux.HandleFunc("GET /reader", page)
	mux.HandleFunc("GET /reader/", page)
	mux.HandleFunc("GET /api/v1/reader/library", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pageNo, _ := strconv.Atoi(q.Get("page"))
		size, _ := strconv.Atoi(q.Get("pageSize"))
		if size < 1 || size > 60 {
			size = 30
		}
		result, e := s.Downloads.ReaderLibrary(pageNo, size, q.Get("q"), q["tag"]...)
		if e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, result)
	})
	mux.HandleFunc("GET /api/v1/reader/works/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil || id <= 0 {
			failStatus(w, http.StatusBadRequest, "BAD_REQUEST", "无效 ID")
			return
		}
		work, e := s.Downloads.ReaderWork(id)
		if e != nil {
			readerError(w, e)
			return
		}
		jsonOut(w, work)
	})
	mux.HandleFunc("GET /api/v1/reader/works/{id}/cover", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil || id <= 0 {
			failStatus(w, http.StatusBadRequest, "BAD_REQUEST", "无效 ID")
			return
		}
		// 検索画面で取得済みの小さなサムネイルがあれば優先し、なければ先頭ページを使う。
		cached := filepath.Join(s.StateDir, "covers", strconv.Itoa(id)+".webp")
		if info, e := os.Stat(cached); e == nil && info.Mode().IsRegular() && info.Size() > 0 {
			serveImage(w, r, cached)
			return
		}
		path, e := s.Downloads.ReaderCoverFile(id)
		if e != nil {
			readerError(w, e)
			return
		}
		serveImage(w, r, path)
	})
	mux.HandleFunc("GET /api/v1/reader/works/{id}/pages/{n}", func(w http.ResponseWriter, r *http.Request) {
		id, e1 := strconv.Atoi(r.PathValue("id"))
		n, e2 := strconv.Atoi(r.PathValue("n"))
		if e1 != nil || e2 != nil || id <= 0 {
			failStatus(w, http.StatusBadRequest, "BAD_REQUEST", "无效参数")
			return
		}
		path, e := s.Downloads.ReaderPageFile(id, n)
		if e != nil {
			readerError(w, e)
			return
		}
		serveImage(w, r, path)
	})
	// 管理画面向け：手機で開くための閲覧リンク（管理用トークンが必要）。
	mux.HandleFunc("GET /api/v1/mobile-link", func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(s.Addr)
		jsonOut(w, map[string]any{"lan": s.LAN, "urls": s.ReaderLinks(), "localUrl": fmt.Sprintf("http://127.0.0.1:%s/reader?token=%s", port, s.ReaderToken)})
	})
}

// ReaderLinks は LAN モードでの閲覧リンク（本機の私有アドレスごと）を返す。LAN モードでなければ空。
func (s *Server) ReaderLinks() []string {
	links := []string{}
	if !s.LAN {
		return links
	}
	_, port, _ := net.SplitHostPort(s.Addr)
	for _, ip := range privateIPv4s() {
		links = append(links, fmt.Sprintf("http://%s:%s/reader?token=%s", ip, port, s.ReaderToken))
	}
	return links
}

func (s *Server) readerCookie() *http.Cookie {
	return &http.Cookie{Name: readerCookie, Value: s.ReaderToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int((90 * 24 * time.Hour).Seconds())}
}
