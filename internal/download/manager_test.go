package download

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hitomi-downloader/internal/hitomi"
)

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testPNG() []byte {
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 3, 3))
	im.Set(1, 1, color.White)
	_ = png.Encode(&b, im)
	return b.Bytes()
}
func await(t *testing.T, m *Manager, id int) Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		_, running := m.cancel[id]
		v := *m.tasks[id]
		m.mu.Unlock()
		if !running {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not terminate")
	return Task{}
}
func fixture(t *testing.T) (*Manager, *int, *sync.Mutex) {
	t.Helper()
	counter := 0
	mu := &sync.Mutex{}
	client, _ := hitomi.New("direct")
	payload := testPNG()
	client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		var b []byte
		switch {
		case strings.HasSuffix(r.URL.Path, ".js") && strings.Contains(r.URL.Path, "galleries"):
			b = []byte(`var galleryinfo = {"id":"42","title":"Fixture","files":[{"name":"a.png","hash":"` + strings.Repeat("a", 64) + `"},{"name":"b.png","hash":"` + strings.Repeat("b", 64) + `"}]}`)
		case strings.HasSuffix(r.URL.Path, "gg.js"):
			b = []byte("var o = 0; case 1: o = 1; break; b: '123/'")
		default:
			mu.Lock()
			counter++
			mu.Unlock()
			b = payload
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: r}, nil
	})
	m, e := New(client, filepath.Join(t.TempDir(), "downloads"), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	return m, &counter, mu
}
func TestDownloadVerifyAndRepairOnlyDamagedPages(t *testing.T) {
	m, count, mu := fixture(t)
	if e := m.Start(42, "", "download"); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 42)
	if task.State != "completed" || task.Done != 2 {
		t.Fatalf("%+v", task)
	}
	meta, _, e := readMetadata(task.Directory)
	if e != nil || len(meta.Pages) != 2 {
		t.Fatalf("%+v %v", meta, e)
	}
	for _, p := range meta.Pages {
		if p.Format != "png" || verify(task.Directory, p) != nil {
			t.Fatalf("invalid page %+v", p)
		}
	}
	mu.Lock()
	before := *count
	mu.Unlock()
	if e = m.Repair(task.Directory); e != nil {
		t.Fatal(e)
	}
	if task = await(t, m, 42); task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	mu.Lock()
	after := *count
	mu.Unlock()
	if after != before {
		t.Fatal("complete verification downloaded images")
	}
	if e = os.WriteFile(filepath.Join(task.Directory, meta.Pages[0].Name), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = m.Repair(task.Directory); e != nil {
		t.Fatal(e)
	}
	task = await(t, m, 42)
	mu.Lock()
	after = *count
	mu.Unlock()
	if task.State != "completed" || after != before+1 {
		t.Fatalf("%+v requests %d -> %d", task, before, after)
	}
	if e = os.Remove(filepath.Join(task.Directory, meta.Pages[1].Name)); e != nil {
		t.Fatal(e)
	}
	if e = m.Repair(task.Directory); e != nil {
		t.Fatal(e)
	}
	if task = await(t, m, 42); task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	entries, warnings, e := m.Scan()
	if e != nil || len(entries) != 1 || len(warnings) != 0 {
		t.Fatalf("%v %v %v", entries, warnings, e)
	}
}
func TestTruncatedImageNotCommitted(t *testing.T) {
	dir := t.TempDir()
	b := testPNG()
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b) + 10)}
	if _, e := writeImage(resp, dir, 0, hitomi.File{}, "png"); e == nil {
		t.Fatal("accepted truncated response")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("left invalid or partial file")
	}
	resp = &http.Response{Body: io.NopCloser(bytes.NewReader(b[:len(b)/2])), ContentLength: -1}
	if _, e := writeImage(resp, dir, 0, hitomi.File{}, "png"); e == nil {
		t.Fatal("accepted corrupt PNG")
	}
}
func TestRestartPausesActiveTasks(t *testing.T) {
	state := t.TempDir()
	if e := AtomicJSON(filepath.Join(state, "tasks", "42.json"), Task{ID: 42, State: "running"}); e != nil {
		t.Fatal(e)
	}
	client, _ := hitomi.New("direct")
	m, e := New(client, t.TempDir(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if m.List()[0].State != "paused" {
		t.Fatal(m.List())
	}
}
func TestScanWarnsAboutBadMetadata(t *testing.T) {
	m, _, _ := fixture(t)
	path := filepath.Join(m.Root(), "bad")
	_ = os.MkdirAll(path, 0755)
	_ = os.WriteFile(filepath.Join(path, "metadata.json"), []byte("{"), 0600)
	entries, warnings, e := m.Scan()
	if e != nil || len(entries) != 0 || len(warnings) != 1 {
		t.Fatalf("%v %v %v", entries, warnings, e)
	}
}
func TestRepairCannotEscapeRoot(t *testing.T) {
	m, _, _ := fixture(t)
	if e := m.Repair(t.TempDir()); e == nil {
		t.Fatal("accepted external path")
	}
}
func TestFailedImageRecordsSpecificPage(t *testing.T) {
	m, _, _ := fixture(t)
	old := m.client.HTTP.Transport
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".png") {
			return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("bad image request")), Request: r}, nil
		}
		return old.RoundTrip(r)
	})
	if e := m.Start(42, "", "download"); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 42)
	if task.State != "failed" || len(task.Errors) != 2 || task.Errors[1] == "" || task.Errors[2] == "" {
		t.Fatalf("%+v", task)
	}
}
func TestMetadataRetainsRawGallery(t *testing.T) {
	m, _, _ := fixture(t)
	_ = m.Start(42, "", "download")
	task := await(t, m, 42)
	meta, g, e := readMetadata(task.Directory)
	if e != nil {
		t.Fatal(e)
	}
	var raw map[string]json.RawMessage
	if e = json.Unmarshal(meta.Gallery, &raw); e != nil || g.Number() != 42 || raw["title"] == nil {
		t.Fatal(e)
	}
}

func TestPauseCancelsWorkersAndResumeReusesFiles(t *testing.T) {
	m, _, _ := fixture(t)
	old := m.client.HTTP.Transport
	entered := make(chan struct{}, 2)
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".png") {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return old.RoundTrip(r)
	})
	if e := m.Start(42, "", "download"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	if e := m.Control(42, "pause"); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 42)
	if task.State != "paused" {
		t.Fatalf("%+v", task)
	}
	m.client.HTTP.Transport = old
	if e := m.Control(42, "resume"); e != nil {
		t.Fatal(e)
	}
	task = await(t, m, 42)
	if task.State != "completed" || task.Done != 2 {
		t.Fatalf("%+v", task)
	}
}

func TestUnavailableOriginalFallsBackAndRecordsReason(t *testing.T) {
	m, _, _ := fixture(t)
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		status := 200
		b := []byte("var o = 0; case 1: o = 1; break; b: '123/'")
		if strings.HasSuffix(r.URL.Path, ".png") {
			status = 404
			b = nil
		}
		if strings.HasSuffix(r.URL.Path, ".avif") {
			b = []byte("\x00\x00\x00\x18ftypavif\x00\x00\x00\x00avifmif1")
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: r}, nil
	})
	p, e := m.fetch(context.Background(), t.TempDir(), 0, hitomi.File{Name: "source.png", Hash: strings.Repeat("a", 64), HasAvif: 1})
	if e != nil || p.Format != "avif" || !strings.Contains(p.Fallback, "png") {
		t.Fatalf("%+v %v", p, e)
	}
}

func TestCloseCancelsDownloadAndPersistsPausedState(t *testing.T) {
	m, _, _ := fixture(t)
	old := m.client.HTTP.Transport
	entered := make(chan struct{}, 2)
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".png") {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return old.RoundTrip(r)
	})
	if e := m.Start(42, "", "download"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	m.Close()
	b, e := os.ReadFile(filepath.Join(m.state, "tasks", "42.json"))
	if e != nil {
		t.Fatal(e)
	}
	var task Task
	if e = json.Unmarshal(b, &task); e != nil {
		t.Fatal(e)
	}
	if task.State != "paused" {
		t.Fatalf("%+v", task)
	}
	if e = m.Start(42, "", "download"); e == nil {
		t.Fatal("accepted task after Close")
	}
}

// 原图用のホストが存在しない（実際の Hitomi と同じ）場合でも、派生形式に回退して取得できる。
func TestMissingOriginalHostFallsBackWithoutRetryDelay(t *testing.T) {
	m, _, _ := fixture(t)
	old := m.client.HTTP.Transport
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		host := r.URL.Hostname()
		if strings.HasPrefix(host, "1.") || strings.HasPrefix(host, "2.") || strings.HasPrefix(host, "j") {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		if strings.HasSuffix(r.URL.Path, ".avif") {
			b := []byte("\x00\x00\x00\x18ftypavif\x00\x00\x00\x00avifmif1")
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: r}, nil
		}
		return old.RoundTrip(r)
	})
	start := time.Now()
	p, e := m.fetch(context.Background(), t.TempDir(), 0, hitomi.File{Name: "source.png", Hash: strings.Repeat("a", 64), HasJxl: 1, HasAvif: 1})
	if e != nil || p.Format != "avif" || !strings.Contains(p.Fallback, "域名无法解析") {
		t.Fatalf("%+v %v", p, e)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("DNS failure was retried with backoff")
	}
}
