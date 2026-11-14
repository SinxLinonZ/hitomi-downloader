package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"hitomi-downloader/internal/download"
	"hitomi-downloader/internal/hitomi"
	"hitomi-downloader/web"
)

type Config struct {
	DownloadDir string `json:"downloadDir"`
}
type searchSession struct {
	IDs []int
	At  time.Time
}
type Server struct {
	Client       *hitomi.Client
	Downloads    *download.Manager
	Token        string
	StateDir     string
	mu           sync.Mutex
	sessions     map[string]searchSession
	settingsMu   sync.Mutex
	shutdownOnce sync.Once
	shutdown     chan struct{}
}

func New(c *hitomi.Client, m *download.Manager, state string) *Server {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return &Server{Client: c, Downloads: m, Token: hex.EncodeToString(b), StateDir: state, sessions: map[string]searchSession{}, shutdown: make(chan struct{})}
}

func (s *Server) RequestShutdown()                   { s.shutdownOnce.Do(func() { close(s.shutdown) }) }
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdown }

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, e error) {
	status := http.StatusBadGateway
	code := "OPERATION_FAILED"
	retry := false
	var he *hitomi.Error
	if errors.As(e, &he) {
		code = he.Code
		retry = he.Retryable
	}
	if errors.Is(e, context.Canceled) {
		status = 499
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": e.Error(), "retryable": retry}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, map[string]bool{"ok": true})
		// 終了通知より先に応答を送信し、呼び出し元が結果を受け取れるようにする。
		_ = http.NewResponseController(w).Flush()
		s.RequestShutdown()
	})
	mux.HandleFunc("GET /api/v1/search", s.search)
	mux.HandleFunc("GET /api/v1/galleries/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil || id <= 0 {
			fail(w, fmt.Errorf("无效 ID"))
			return
		}
		g, e := s.Client.Gallery(r.Context(), id)
		if e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, json.RawMessage(g.Raw))
	})
	mux.HandleFunc("GET /api/v1/galleries/{id}/related", s.related)
	mux.HandleFunc("GET /api/v1/covers/{id}", s.cover)
	mux.HandleFunc("GET /api/v1/downloads", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, s.Downloads.List()) })
	mux.HandleFunc("POST /api/v1/downloads/pause-all", func(w http.ResponseWriter, r *http.Request) {
		result, e := s.Downloads.ControlAll(r.Context(), "pause")
		if e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, result)
	})
	mux.HandleFunc("POST /api/v1/downloads/resume-all", func(w http.ResponseWriter, r *http.Request) {
		result, e := s.Downloads.ControlAll(r.Context(), "resume")
		if e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, result)
	})
	mux.HandleFunc("POST /api/v1/downloads/clear-completed", func(w http.ResponseWriter, r *http.Request) {
		result, e := s.Downloads.RemoveCompleted(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, result)
	})
	mux.HandleFunc("POST /api/v1/downloads", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID int `json:"id"`
		}
		if e := decode(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		if e := s.Downloads.Start(body.ID, "", "download"); e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/downloads/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.PathValue("id"))
		if e := s.Downloads.Control(id, r.PathValue("action")); e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/library", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		result, e := s.Downloads.Library(page, size, r.URL.Query().Get("q"), r.URL.Query().Get("kind"), r.URL.Query()["tag"]...)
		if e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, result)
	})
	mux.HandleFunc("POST /api/v1/library/scan", func(w http.ResponseWriter, r *http.Request) {
		if e := s.Downloads.StartScan(true); e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/library/scan/cancel", func(w http.ResponseWriter, r *http.Request) {
		s.Downloads.CancelScan()
		jsonOut(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/library/migrate", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Directories []string `json:"directories"`
		}
		if e := decode(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		if len(body.Directories) == 0 || len(body.Directories) > 200 {
			fail(w, fmt.Errorf("请选择 1 至 200 个作品迁移"))
			return
		}
		accepted := []string{}
		problems := map[string]string{}
		seen := map[string]bool{}
		for _, dir := range body.Directories {
			if r.Context().Err() != nil {
				return
			}
			if seen[dir] {
				continue
			}
			seen[dir] = true
			if e := s.Downloads.Migrate(dir); e != nil {
				problems[dir] = e.Error()
			} else {
				accepted = append(accepted, dir)
			}
		}
		jsonOut(w, map[string]any{"accepted": accepted, "errors": problems})
	})
	mux.HandleFunc("GET /api/v1/library/migration-targets", func(w http.ResponseWriter, r *http.Request) {
		targets, e := s.Downloads.MigrationTargets()
		if e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, targets)
	})
	mux.HandleFunc("GET /api/v1/library/cover", func(w http.ResponseWriter, r *http.Request) {
		path, e := s.Downloads.LocalCover(r.URL.Query().Get("directory"))
		if e != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=3600")
		http.ServeFile(w, r, path)
	})
	mux.HandleFunc("POST /api/v1/library/repair", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Directory string `json:"directory"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, e)
			return
		}
		if e := s.Downloads.Repair(b.Directory); e != nil {
			fail(w, e)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/library/repair/batch", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Directories []string `json:"directories"`
		}
		if e := decode(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		if len(body.Directories) == 0 || len(body.Directories) > 200 {
			fail(w, fmt.Errorf("请选择 1 至 200 个作品"))
			return
		}
		accepted := []string{}
		problems := map[string]string{}
		for _, dir := range body.Directories {
			if r.Context().Err() != nil {
				return
			}
			if e := s.Downloads.Repair(dir); e != nil {
				problems[dir] = e.Error()
			} else {
				accepted = append(accepted, dir)
			}
		}
		jsonOut(w, map[string]any{"accepted": accepted, "errors": problems})
	})
	mux.HandleFunc("POST /api/v1/library/update", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Directories []string `json:"directories"`
		}
		if e := decode(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		if len(body.Directories) == 0 || len(body.Directories) > 200 {
			fail(w, fmt.Errorf("请选择 1 至 200 个作品"))
			return
		}
		accepted := []string{}
		problems := map[string]string{}
		for _, dir := range body.Directories {
			if r.Context().Err() != nil {
				return
			}
			if e := s.Downloads.Update(dir); e != nil {
				problems[dir] = e.Error()
			} else {
				accepted = append(accepted, dir)
			}
		}
		jsonOut(w, map[string]any{"accepted": accepted, "errors": problems})
	})
	mux.HandleFunc("GET /api/v1/settings", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, Config{s.Downloads.Root()}) })
	mux.HandleFunc("PUT /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		s.settingsMu.Lock()
		defer s.settingsMu.Unlock()
		var c Config
		if e := decode(w, r, &c); e != nil {
			fail(w, e)
			return
		}
		if !filepath.IsAbs(c.DownloadDir) {
			fail(w, fmt.Errorf("请填写绝对路径"))
			return
		}
		old := s.Downloads.Root()
		if e := s.Downloads.SetRoot(c.DownloadDir); e != nil {
			fail(w, e)
			return
		}
		if e := download.AtomicJSON(filepath.Join(s.StateDir, "config.json"), c); e != nil {
			_ = s.Downloads.SetRoot(old)
			fail(w, e)
			return
		}
		jsonOut(w, c)
	})
	mux.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		notifyShutdown := func() {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Second))
			_, _ = fmt.Fprint(w, "event: shutdown\ndata: {}\n\n")
			f.Flush()
		}
		// 変化がない間は再送せず、15 秒ごとの keep-alive だけを送る。
		var last []byte
		lastWrite := time.Time{}
		for {
			select {
			case <-s.shutdown:
				notifyShutdown()
				return
			default:
			}
			b, _ := json.Marshal(s.Downloads.List())
			if !bytes.Equal(b, last) || time.Since(lastWrite) >= 15*time.Second {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
				var e error
				if bytes.Equal(b, last) {
					_, e = fmt.Fprint(w, ": keep-alive\n\n")
				} else {
					_, e = fmt.Fprintf(w, "data: %s\n\n", b)
				}
				if e != nil {
					return
				}
				f.Flush()
				last = b
				lastWrite = time.Now()
			}
			select {
			case <-s.shutdown:
				notifyShutdown()
				return
			case <-r.Context().Done():
				select {
				case <-s.shutdown:
					notifyShutdown()
				default:
				}
				return
			case <-ticker.C:
			}
		}
	})
	assets, _ := fs.Sub(web.Assets, "dist")
	files := http.FileServer(http.FS(assets))
	mux.Handle("/", files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, e := net.SplitHostPort(r.Host)
		if e != nil {
			host = r.Host
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "invalid host", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		if token := r.URL.Query().Get("token"); token != "" {
			if subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) != 1 {
				http.Error(w, "invalid token", 403)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "hitomi_session", Value: s.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			cookie, e := r.Cookie("hitomi_session")
			if e != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.Token)) != 1 {
				http.Error(w, "请使用 CLI 打印的启动链接打开界面", 401)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Host != r.Host {
					http.Error(w, "invalid origin", 403)
					return
				}
			}
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	sessionID := r.URL.Query().Get("session")
	var ids []int
	if sessionID != "" {
		s.mu.Lock()
		session, ok := s.sessions[sessionID]
		s.mu.Unlock()
		if !ok || time.Since(session.At) > 30*time.Minute {
			fail(w, fmt.Errorf("搜索已过期，请重新搜索"))
			return
		}
		ids = session.IDs
	} else {
		var e error
		ids, e = s.Client.Search(ctx, r.URL.Query().Get("q"))
		if e != nil {
			fail(w, e)
			return
		}
		b := make([]byte, 16)
		if _, e = rand.Read(b); e != nil {
			fail(w, e)
			return
		}
		sessionID = hex.EncodeToString(b)
		s.mu.Lock()
		if len(s.sessions) >= 20 {
			old := ""
			at := time.Now()
			for k, v := range s.sessions {
				if v.At.Before(at) {
					at = v.At
					old = k
				}
			}
			delete(s.sessions, old)
		}
		s.sessions[sessionID] = searchSession{ids, time.Now()}
		s.mu.Unlock()
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	start := (page - 1) * 24
	if start < 0 || start > len(ids) {
		fail(w, fmt.Errorf("页码超出范围"))
		return
	}
	end := start + 24
	if end > len(ids) {
		end = len(ids)
	}
	items := s.galleryItems(ctx, ids[start:end])
	jsonOut(w, map[string]any{"session": sessionID, "page": page, "total": len(ids), "items": items})
}

type galleryItem struct {
	ID      int             `json:"id"`
	Gallery json.RawMessage `json:"gallery,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// 複数の gallery 情報を並行取得する。個別の失敗は項目ごとの error に入れ、他の結果は保持する。
func (s *Server) galleryItems(ctx context.Context, ids []int) []galleryItem {
	items := make([]galleryItem, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, id := range ids {
		wg.Add(1)
		go func(i, id int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				items[i] = galleryItem{ID: id, Error: ctx.Err().Error()}
				return
			}
			g, e := s.Client.Gallery(ctx, id)
			items[i].ID = id
			if e != nil {
				items[i].Error = e.Error()
			} else {
				items[i].Gallery = g.Raw
			}
		}(i, id)
	}
	wg.Wait()
	return items
}

// 作品詳細に表示する関連作品（gallery 情報の related）を返す。
func (s *Server) related(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.Atoi(r.PathValue("id"))
	if e != nil || id <= 0 {
		fail(w, fmt.Errorf("无效 ID"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	g, e := s.Client.Gallery(ctx, id)
	if e != nil {
		fail(w, e)
		return
	}
	var meta struct {
		Related []int `json:"related"`
	}
	ids := []int{}
	if json.Unmarshal(g.Raw, &meta) == nil {
		seen := map[int]bool{id: true}
		for _, v := range meta.Related {
			if v > 0 && !seen[v] && len(ids) < 40 {
				seen[v] = true
				ids = append(ids, v)
			}
		}
	}
	jsonOut(w, map[string]any{"items": s.galleryItems(ctx, ids)})
}
func (s *Server) cover(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.Atoi(r.PathValue("id"))
	if e != nil || id <= 0 {
		fail(w, fmt.Errorf("无效 ID"))
		return
	}
	cache := filepath.Join(s.StateDir, "covers", strconv.Itoa(id)+".webp")
	if info, e := os.Stat(cache); e == nil && time.Since(info.ModTime()) < 24*time.Hour {
		http.ServeFile(w, r, cache)
		return
	}
	g, e := s.Client.Gallery(r.Context(), id)
	if e != nil {
		fail(w, e)
		return
	}
	u, e := s.Client.URL(r.Context(), g.Files[0], "thumbnail", false)
	if e != nil {
		fail(w, e)
		return
	}
	b, e := s.Client.Bytes(r.Context(), u, "", 8<<20)
	if e != nil {
		fail(w, e)
		return
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		fail(w, fmt.Errorf("封面不是有效 WebP"))
		return
	}
	if e = os.MkdirAll(filepath.Dir(cache), 0700); e == nil {
		tmp, e := os.CreateTemp(filepath.Dir(cache), ".cover-*")
		if e == nil {
			name := tmp.Name()
			_, e = tmp.Write(b)
			ce := tmp.Close()
			if e == nil && ce == nil {
				_ = os.Rename(name, cache)
			}
			_ = os.Remove(name)
		}
	}
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(b)
}
