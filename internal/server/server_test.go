package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hitomi-downloader/internal/download"
	"hitomi-downloader/internal/hitomi"
)

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func setup(t *testing.T) *Server {
	t.Helper()
	client, _ := hitomi.New("direct")
	m, e := download.New(client, t.TempDir(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	return New(client, m, t.TempDir())
}
func call(s *Server, method, path, body, origin string, authenticated bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	if authenticated {
		r.AddCookie(&http.Cookie{Name: "hitomi_session", Value: s.Token})
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestLocalSessionAndOrigin(t *testing.T) {
	s := setup(t)
	if w := call(s, "GET", "/api/v1/settings", "", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(s, "GET", "/?token="+s.Token, "", "", false); w.Code != 303 || len(w.Result().Cookies()) != 1 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(s, "GET", "/api/v1/settings", "", "", true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call(s, "PUT", "/api/v1/settings", `{"downloadDir":"/tmp"}`, "https://evil.example", true); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestSettingsPersistAndLibraryScans(t *testing.T) {
	s := setup(t)
	dir := t.TempDir()
	b, _ := json.Marshal(Config{DownloadDir: dir})
	w := call(s, "PUT", "/api/v1/settings", string(b), "", true)
	if w.Code != 200 || s.Downloads.Root() != dir {
		t.Fatal(w.Code, w.Body)
	}
	w = call(s, "GET", "/api/v1/library", "", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"entries":[]`) {
		t.Fatal(w.Code, w.Body)
	}
}
func TestSearchKeepsSuccessfulCardsWhenOneGalleryFails(t *testing.T) {
	s := setup(t)
	s.Client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		status := 200
		var b []byte
		switch r.URL.Path {
		case "/n/index-all.nozomi":
			b = make([]byte, 8)
			binary.BigEndian.PutUint32(b, 1)
			binary.BigEndian.PutUint32(b[4:], 2)
		case "/galleries/1.js":
			b = []byte(`var galleryinfo = {"id":1,"title":"Fixture","files":[{"name":"page.png","hash":"` + strings.Repeat("a", 64) + `"}]}`)
		default:
			status = 404
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(b)), Header: http.Header{}, Request: r}, nil
	})
	w := call(s, "GET", "/api/v1/search", "", "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var result struct {
		Total int `json:"total"`
		Items []struct {
			Gallery json.RawMessage `json:"gallery"`
			Error   string          `json:"error"`
		} `json:"items"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Total != 2 || len(result.Items[0].Gallery) == 0 || result.Items[1].Error == "" {
		t.Fatalf("%s", w.Body)
	}
}

func TestRelatedGalleriesKeepFailuresAndSkipSelf(t *testing.T) {
	s := setup(t)
	hash := strings.Repeat("a", 64)
	s.Client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		status := 200
		var b []byte
		switch r.URL.Path {
		case "/galleries/1.js":
			b = []byte(`var galleryinfo = {"id":1,"title":"Main","related":[1,2,3,2],"files":[{"name":"p.png","hash":"` + hash + `"}]}`)
		case "/galleries/2.js":
			b = []byte(`var galleryinfo = {"id":2,"title":"Related","files":[{"name":"p.png","hash":"` + hash + `"}]}`)
		default:
			status = 404
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(b)), Header: http.Header{}, Request: r}, nil
	})
	if w := call(s, "GET", "/api/v1/galleries/1/related", "", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := call(s, "GET", "/api/v1/galleries/1/related", "", "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var result struct {
		Items []struct {
			ID      int             `json:"id"`
			Gallery json.RawMessage `json:"gallery"`
			Error   string          `json:"error"`
		} `json:"items"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	// 自分自身と重複は除外し、取得に失敗した作品も項目として残す。
	if len(result.Items) != 2 || result.Items[0].ID != 2 || len(result.Items[0].Gallery) == 0 || result.Items[1].ID != 3 || result.Items[1].Error == "" {
		t.Fatalf("%s", w.Body)
	}
}

func TestShutdownEndpointRequiresSessionAndSameOrigin(t *testing.T) {
	s := setup(t)
	for _, tc := range []struct {
		authenticated bool
		origin        string
		status        int
	}{{false, "", 401}, {true, "https://evil.example", 403}} {
		w := call(s, "POST", "/api/v1/shutdown", "", tc.origin, tc.authenticated)
		if w.Code != tc.status {
			t.Fatal(w.Code)
		}
		select {
		case <-s.ShutdownRequested():
			t.Fatal("unauthorized shutdown")
		default:
		}
	}
	w := call(s, "POST", "/api/v1/shutdown", "", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatal(w.Code, w.Body)
	}
	select {
	case <-s.ShutdownRequested():
	default:
		t.Fatal("shutdown was not requested")
	}
	w = call(s, "POST", "/api/v1/shutdown", "", "", true)
	if w.Code != 200 {
		t.Fatal("repeated shutdown should be idempotent")
	}
}

func TestManualMigrationBatchAndLocalCover(t *testing.T) {
	s := setup(t)
	s.Client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected network request: %s", r.URL)
		return nil, io.ErrClosedPipe
	})
	dir := filepath.Join(s.Downloads.Root(), "old")
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	var imageData bytes.Buffer
	if e := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "0001.png"), imageData.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	raw := []byte(`{"id":42,"title":"Legacy","artists":["Artist"],"files":[{"name":"source.png","hash":"` + strings.Repeat("a", 64) + `"}]}`)
	if e := os.WriteFile(filepath.Join(dir, "metadata.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]any{"directories": []string{dir, t.TempDir()}})
	w := call(s, "POST", "/api/v1/library/migrate", string(body), "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var result struct {
		Accepted []string          `json:"accepted"`
		Errors   map[string]string `json:"errors"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil || len(result.Accepted) != 1 || len(result.Errors) != 1 {
		t.Fatalf("%s %v", w.Body, e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tasks := s.Downloads.List()
		if len(tasks) > 0 && tasks[0].State == "completed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	tasks := s.Downloads.List()
	if len(tasks) != 1 || tasks[0].State != "completed" {
		t.Fatalf("%+v", tasks)
	}
	if _, e := os.Stat(filepath.Join(dir, "metadata.legacy.json")); e != nil {
		t.Fatal(e)
	}
	w = call(s, "GET", "/api/v1/library/cover?directory="+url.QueryEscape(dir), "", "", true)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), imageData.Bytes()) {
		t.Fatal(w.Code, w.Body)
	}
	w = call(s, "GET", "/api/v1/library/cover?directory="+url.QueryEscape(t.TempDir()), "", "", true)
	if w.Code != 404 {
		t.Fatal("external cover was accessible", w.Code)
	}
	w = call(s, "POST", "/api/v1/library/scan", "{}", "", true)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = call(s, "POST", "/api/v1/library/scan/cancel", "{}", "", true)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestMigrationTargetsEndpointRequiresSessionAndCompletedScan(t *testing.T) {
	s := setup(t)
	if w := call(s, "GET", "/api/v1/library/migration-targets", "", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(s, "GET", "/api/v1/library/migration-targets", "", "", true); w.Code == 200 {
		t.Fatal("snapshot accepted before scan")
	}
	if e := s.Downloads.StartScan(false); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, e := s.Downloads.Library(1, 20, "", "all")
		if e != nil {
			t.Fatal(e)
		}
		if result.Scan.State == "completed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	w := call(s, "GET", "/api/v1/library/migration-targets", "", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"directories":[]`) {
		t.Fatal(w.Code, w.Body)
	}
}

func TestLibrarySupportsTextSearchAndMultipleTagFilters(t *testing.T) {
	s := setup(t)
	for i, namespace := range []string{"female", "male"} {
		dir := filepath.Join(s.Downloads.Root(), namespace)
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(map[string]any{"id": 401 + i, "title": "Example " + namespace, "files": []map[string]string{{"name": "page.png", "hash": strings.Repeat("a", 64)}}, "tags": []map[string]any{{"tag": "digital"}, {"tag": "glasses", namespace: 1}}})
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "metadata.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		page, e := s.Downloads.Library(1, 20, "", "all")
		if e != nil {
			t.Fatal(e)
		}
		if page.Scan.State == "completed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	w := call(s, "GET", "/api/v1/library?q=example&tag=tag%3Adigital&tag=female%3Aglasses", "", "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var result struct {
		Total   int                 `json:"total"`
		Entries []download.Entry    `json:"entries"`
		Tags    []download.TagFacet `json:"tags"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil || result.Total != 1 || len(result.Entries) != 1 || result.Entries[0].ID != 401 || len(result.Entries[0].Tags) != 2 {
		t.Fatalf("%s %v", w.Body, e)
	}
	count := 0
	for _, tag := range result.Tags {
		if tag.Value == "tag:digital" {
			count = tag.Count
		}
	}
	if count != 2 {
		t.Fatalf("tag counts: %+v", result.Tags)
	}
}

func TestQueueBatchEndpointsRequireSessionAndRemoveOnlyTaskRecords(t *testing.T) {
	s := setup(t)
	for _, action := range []string{"pause-all", "resume-all", "clear-completed"} {
		path := "/api/v1/downloads/" + action
		if w := call(s, "POST", path, "{}", "", false); w.Code != 401 {
			t.Fatal(action, w.Code)
		}
		if w := call(s, "POST", path, "{}", "https://evil.example", true); w.Code != 403 {
			t.Fatal(action, w.Code)
		}
		if w := call(s, "POST", path, "{}", "", true); w.Code != 200 {
			t.Fatal(action, w.Code, w.Body)
		}
	}
	state := t.TempDir()
	root := t.TempDir()
	dir := filepath.Join(root, "downloaded-artwork")
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte("preserve metadata"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := download.AtomicJSON(filepath.Join(state, "tasks", "42.json"), download.Task{ID: 42, Root: root, Directory: dir, State: "completed"}); e != nil {
		t.Fatal(e)
	}
	manager, e := download.New(s.Client, root, state)
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	app := New(s.Client, manager, state)
	w := call(app, "POST", "/api/v1/downloads/clear-completed", "{}", "", true)
	var result download.TaskBatchResult
	if e = json.Unmarshal(w.Body.Bytes(), &result); w.Code != 200 || e != nil || result.Affected != 1 {
		t.Fatal(w.Code, w.Body, e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if e != nil || string(b) != "preserve metadata" {
		t.Fatal("API changed downloaded metadata", e)
	}
	if len(manager.List()) != 0 {
		t.Fatal("task was not removed")
	}
}

func TestLibraryUpdateEndpointRequiresSessionAndRejectsOutsideDirectories(t *testing.T) {
	s := setup(t)
	if w := call(s, "POST", "/api/v1/library/update", `{"directories":["/tmp"]}`, "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := call(s, "POST", "/api/v1/library/update", `{"directories":["`+t.TempDir()+`"]}`, "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"accepted":[]`) {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(s, "POST", "/api/v1/library/update", `{"directories":[]}`, "", true); w.Code == 200 {
		t.Fatal("accepted an empty selection")
	}
}

func TestConcurrencySettingsPersistAndValidate(t *testing.T) {
	s := setup(t)
	dir := s.Downloads.Root()
	if w := call(s, "GET", "/api/v1/settings", "", "", true); !strings.Contains(w.Body.String(), `"taskConcurrency":2`) || !strings.Contains(w.Body.String(), `"pageConcurrency":4`) {
		t.Fatalf("defaults missing: %s", w.Body)
	}
	body, _ := json.Marshal(Config{DownloadDir: dir, TaskConcurrency: 3, PageConcurrency: 6})
	if w := call(s, "PUT", "/api/v1/settings", string(body), "", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if tasks, pages := s.Downloads.Concurrency(); tasks != 3 || pages != 6 {
		t.Fatal(tasks, pages)
	}
	saved, _ := os.ReadFile(filepath.Join(s.StateDir, "config.json"))
	if !strings.Contains(string(saved), `"taskConcurrency": 3`) || !strings.Contains(string(saved), `"pageConcurrency": 6`) {
		t.Fatalf("not persisted: %s", saved)
	}
	// 只更新下载目录时，并发数保持不变。
	body, _ = json.Marshal(Config{DownloadDir: dir})
	if w := call(s, "PUT", "/api/v1/settings", string(body), "", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if tasks, pages := s.Downloads.Concurrency(); tasks != 3 || pages != 6 {
		t.Fatal("omitted values reset the concurrency", tasks, pages)
	}
	for _, bad := range []Config{{DownloadDir: dir, TaskConcurrency: 99}, {DownloadDir: dir, PageConcurrency: 99}, {DownloadDir: dir, TaskConcurrency: -1}} {
		body, _ = json.Marshal(bad)
		if w := call(s, "PUT", "/api/v1/settings", string(body), "", true); w.Code == 200 {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if tasks, pages := s.Downloads.Concurrency(); tasks != 3 || pages != 6 {
		t.Fatal("rejected request changed the concurrency", tasks, pages)
	}
}
