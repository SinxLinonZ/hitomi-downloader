package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 指定した接続元・Host・Cookie でリクエストする。
func callAs(s *Server, remote, host, method, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+path, nil)
	r.RemoteAddr = remote
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func adminCookie(s *Server) *http.Cookie { return &http.Cookie{Name: "hitomi_session", Value: s.Token} }
func readerCookieFor(s *Server) *http.Cookie {
	return &http.Cookie{Name: readerCookie, Value: s.ReaderToken}
}

func TestReaderTokenIsSeparateFromAdminToken(t *testing.T) {
	s := setup(t)
	if s.Token == s.ReaderToken || len(s.ReaderToken) < 32 {
		t.Fatal("reader token must be an independent secret")
	}
	local := "127.0.0.1:50000"
	if w := callAs(s, local, "127.0.0.1:8080", "GET", "/api/v1/reader/library"); w.Code != 401 {
		t.Fatalf("unauthenticated reader API: %d", w.Code)
	}
	if w := callAs(s, local, "127.0.0.1:8080", "GET", "/api/v1/reader/library", readerCookieFor(s)); w.Code != 200 {
		t.Fatalf("reader cookie rejected: %d %s", w.Code, w.Body)
	}
	// 閲覧用の資格情報では、ダウンロードや設定の API は使えない。
	for _, path := range []string{"/api/v1/settings", "/api/v1/downloads", "/api/v1/mobile-link", "/api/v1/library"} {
		if w := callAs(s, local, "127.0.0.1:8080", "GET", path, readerCookieFor(s)); w.Code != 401 {
			t.Fatalf("reader cookie reached %s: %d", path, w.Code)
		}
	}
	// 管理用の Cookie は閲覧 API にも使える。
	if w := callAs(s, local, "127.0.0.1:8080", "GET", "/api/v1/reader/library", adminCookie(s)); w.Code != 200 {
		t.Fatalf("admin cookie rejected: %d", w.Code)
	}
	w := callAs(s, local, "127.0.0.1:8080", "GET", "/?token="+s.ReaderToken)
	if w.Code != 303 || w.Header().Get("Location") != "/reader" || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Name != readerCookie || !w.Result().Cookies()[0].HttpOnly {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

func TestReaderTokenIsStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	if a, b := loadReaderToken(dir), loadReaderToken(dir); a != b || len(a) < 32 {
		t.Fatal("reader token changed between loads")
	}
	info, e := os.Stat(filepath.Join(dir, "reader-token"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("token file permissions: %v %v", info, e)
	}
}

func TestLANClientsAreReadOnlyAndHostIsChecked(t *testing.T) {
	s := setup(t)
	remote, host := "192.168.1.20:51000", "192.168.1.5:8080"
	// LAN モードでなければ、IP アドレスの Host は拒否する。
	if w := callAs(s, remote, host, "GET", "/reader"); w.Code != 403 {
		t.Fatalf("LAN host accepted while LAN mode is off: %d", w.Code)
	}
	s.LAN = true
	if w := callAs(s, remote, "evil.example:8080", "GET", "/reader"); w.Code != 403 {
		t.Fatalf("arbitrary host accepted: %d", w.Code)
	}
	// 管理用トークンは LAN の端末からは使えない。
	if w := callAs(s, remote, host, "GET", "/?token="+s.Token); w.Code != 403 {
		t.Fatalf("admin token accepted from the LAN: %d", w.Code)
	}
	if w := callAs(s, remote, host, "GET", "/api/v1/reader/library"); w.Code != 401 {
		t.Fatalf("%d", w.Code)
	}
	w := callAs(s, remote, host, "GET", "/?token="+s.ReaderToken)
	if w.Code != 303 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("%d", w.Code)
	}
	if w := callAs(s, remote, host, "GET", "/api/v1/reader/library", readerCookieFor(s)); w.Code != 200 {
		t.Fatalf("reader rejected on the LAN: %d %s", w.Code, w.Body)
	}
	// 管理用 Cookie が漏れても、LAN の端末からは管理 API を使えない。
	for _, path := range []string{"/api/v1/settings", "/api/v1/downloads", "/api/v1/shutdown"} {
		if w := callAs(s, remote, host, "GET", path, adminCookie(s), readerCookieFor(s)); w.Code != 403 {
			t.Fatalf("%s reachable from the LAN: %d", path, w.Code)
		}
	}
	if w := callAs(s, remote, host, "POST", "/api/v1/downloads", adminCookie(s)); w.Code != 403 {
		t.Fatalf("download API reachable from the LAN: %d", w.Code)
	}
	// 管理画面のトップは閲覧ページへ誘導する。
	if w := callAs(s, remote, host, "GET", "/"); w.Code != 303 || w.Header().Get("Location") != "/reader" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	if w := callAs(s, remote, host, "GET", "/reader"); w.Code == 303 || w.Code == 403 {
		t.Fatalf("reader page blocked: %d", w.Code)
	}
}

func readerFixture(t *testing.T, s *Server) []byte {
	t.Helper()
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 3)))
	data := buf.Bytes()
	dir := filepath.Join(s.Downloads.Root(), "42 - Sample")
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(data)
	meta := map[string]any{
		"schemaVersion": 1,
		"gallery": map[string]any{"id": 42, "title": "Sample", "japanese_title": "サンプル", "language": "japanese", "type": "manga",
			"artists": []map[string]string{{"artist": "someone"}},
			"tags":    []map[string]any{{"tag": "glasses", "female": 1}},
			"files":   []map[string]any{{"name": "a.png", "hash": strings.Repeat("a", 64), "width": 2, "height": 3}}},
		"pages":     []map[string]any{{"index": 0, "name": "0001.png", "sourceHash": strings.Repeat("a", 64), "format": "png", "bytes": len(data), "sha256": hex.EncodeToString(sum[:])}},
		"updatedAt": time.Now(),
	}
	b, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(dir, "metadata.json"), b, 0600)
	_ = os.WriteFile(filepath.Join(dir, "0001.png"), data, 0600)
	return data
}

func TestReaderAPIListsFiltersAndServesPages(t *testing.T) {
	s := setup(t)
	data := readerFixture(t, s)
	get := func(path string) *httptest.ResponseRecorder {
		return callAs(s, "127.0.0.1:50000", "127.0.0.1:8080", "GET", path, readerCookieFor(s))
	}
	var list struct {
		Entries  []map[string]any         `json:"entries"`
		Total    int                      `json:"total"`
		Scanning bool                     `json:"scanning"`
		Tags     []struct{ Value string } `json:"tags"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := get("/api/v1/reader/library")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		_ = json.Unmarshal(w.Body.Bytes(), &list)
		if !list.Scanning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if list.Total != 1 || len(list.Tags) != 1 || list.Tags[0].Value != "female:glasses" {
		t.Fatalf("%+v", list)
	}
	if body := get("/api/v1/reader/library").Body.String(); strings.Contains(body, "directory") || strings.Contains(body, s.Downloads.Root()) {
		t.Fatalf("filesystem path leaked: %s", body)
	}
	var filtered struct{ Total int }
	_ = json.Unmarshal(get("/api/v1/reader/library?tag=female:glasses").Body.Bytes(), &filtered)
	if filtered.Total != 1 {
		t.Fatal(filtered)
	}
	_ = json.Unmarshal(get("/api/v1/reader/library?q=nomatch").Body.Bytes(), &filtered)
	if filtered.Total != 0 {
		t.Fatal(filtered)
	}
	w := get("/api/v1/reader/works/42")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"pageList":[{"w":2,"h":3}]`) {
		t.Fatal(w.Code, w.Body)
	}
	w = get("/api/v1/reader/works/42/pages/1")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), data) || !strings.Contains(w.Header().Get("Cache-Control"), "private") {
		t.Fatal(w.Code, w.Header())
	}
	if w = get("/api/v1/reader/works/42/cover"); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/api/v1/reader/works/42/pages/2", "/api/v1/reader/works/42/pages/0", "/api/v1/reader/works/42/pages/-1", "/api/v1/reader/works/999", "/api/v1/reader/works/999/cover"} {
		if w := get(path); w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/v1/reader/works/abc", "/api/v1/reader/works/42/pages/x", "/api/v1/reader/works/0"} {
		if w := get(path); w.Code != 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}
