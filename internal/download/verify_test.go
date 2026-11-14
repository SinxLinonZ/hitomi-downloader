package download

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 画像以外の gallery 応答だけを差し替える。gallery が空文字なら 404、"offline" なら通信エラー。
func serveGallery(m *Manager, gallery *string, images *atomic.Int32) {
	old := m.client.HTTP.Transport
	m.client.Backoff = time.Millisecond
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".js") && strings.Contains(r.URL.Path, "galleries") {
			switch *gallery {
			case "offline":
				return nil, io.ErrUnexpectedEOF
			case "":
				return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
			}
			b := []byte("var galleryinfo = " + *gallery)
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: r}, nil
		}
		if !strings.HasSuffix(r.URL.Path, "gg.js") {
			images.Add(1)
		}
		return old.RoundTrip(r)
	})
}

func galleryJSON(hashes ...string) string {
	files := []string{}
	for _, h := range hashes {
		files = append(files, `{"name":"x.png","hash":"`+strings.Repeat(h, 64)+`"}`)
	}
	return `{"id":"42","title":"Fixture","files":[` + strings.Join(files, ",") + `]}`
}

func downloaded(t *testing.T) (*Manager, Task) {
	t.Helper()
	m, _, _ := fixture(t)
	if e := m.Start(42, "", "download"); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 42)
	if task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	return m, task
}

func TestVerifyReportsUpstreamReplacementWithoutTouchingLocalFiles(t *testing.T) {
	m, task := downloaded(t)
	gallery := galleryJSON("a", "c") // 第 2 页が上流で差し替えられた
	var images atomic.Int32
	serveGallery(m, &gallery, &images)
	before, _ := os.ReadFile(filepath.Join(task.Directory, "0002.png"))
	if e := m.Repair(task.Directory); e != nil {
		t.Fatal(e)
	}
	task = await(t, m, 42)
	if task.State != "completed" || !strings.Contains(task.Warning, "替换 1 页") || images.Load() != 0 {
		t.Fatalf("%+v images=%d", task, images.Load())
	}
	meta, _, _ := readMetadata(task.Directory)
	if meta.Upstream == nil || meta.Upstream.Status != "changed" || len(meta.Upstream.Changed) != 1 || meta.Upstream.Changed[0] != 2 {
		t.Fatalf("%+v", meta.Upstream)
	}
	after, _ := os.ReadFile(filepath.Join(task.Directory, "0002.png"))
	if !bytes.Equal(before, after) {
		t.Fatal("verification overwrote local file")
	}
	entries, _, _ := m.Scan()
	if len(entries) != 1 || entries[0].Upstream != "changed" {
		t.Fatalf("%+v", entries)
	}
}

func TestVerifyDoesNotRedownloadOldVersionOfReplacedPage(t *testing.T) {
	m, task := downloaded(t)
	gallery := galleryJSON("a", "c")
	var images atomic.Int32
	serveGallery(m, &gallery, &images)
	_ = os.Remove(filepath.Join(task.Directory, "0002.png"))
	_ = m.Repair(task.Directory)
	task = await(t, m, 42)
	if task.State != "failed" || task.Errors[2] == "" || images.Load() != 0 {
		t.Fatalf("%+v images=%d", task, images.Load())
	}
}

func TestVerifyRepairsLocalDamageWhenUpstreamUnchanged(t *testing.T) {
	m, task := downloaded(t)
	gallery := galleryJSON("a", "b")
	var images atomic.Int32
	serveGallery(m, &gallery, &images)
	_ = os.WriteFile(filepath.Join(task.Directory, "0001.png"), []byte("broken"), 0600)
	_ = m.Repair(task.Directory)
	task = await(t, m, 42)
	if task.State != "completed" || task.Warning != "" || images.Load() != 1 {
		t.Fatalf("%+v images=%d", task, images.Load())
	}
	meta, _, _ := readMetadata(task.Directory)
	if meta.Upstream == nil || meta.Upstream.Status != "synced" {
		t.Fatalf("%+v", meta.Upstream)
	}
}

func TestVerifyMarksDeletedUpstreamAndKeepsLocalContent(t *testing.T) {
	m, task := downloaded(t)
	gallery := "" // 404
	var images atomic.Int32
	serveGallery(m, &gallery, &images)
	_ = os.Remove(filepath.Join(task.Directory, "0002.png"))
	_ = m.Repair(task.Directory)
	task = await(t, m, 42)
	if task.State != "failed" || !strings.Contains(task.Warning, "已找不到") || images.Load() != 0 {
		t.Fatalf("%+v images=%d", task, images.Load())
	}
	meta, _, _ := readMetadata(task.Directory)
	if meta.Upstream == nil || meta.Upstream.Status != "deleted" {
		t.Fatalf("%+v", meta.Upstream)
	}
	if _, e := os.Stat(filepath.Join(task.Directory, "0001.png")); e != nil {
		t.Fatal("local page removed")
	}
}

func TestVerifyUnknownUpstreamKeepsPreviousStatus(t *testing.T) {
	m, task := downloaded(t)
	gallery := galleryJSON("a", "c")
	var images atomic.Int32
	serveGallery(m, &gallery, &images)
	_ = m.Repair(task.Directory)
	await(t, m, 42)
	gallery = "offline"
	_ = m.Repair(task.Directory)
	task = await(t, m, 42)
	if task.State != "completed" || !strings.Contains(task.Warning, "仅完成本地校验") {
		t.Fatalf("%+v", task)
	}
	meta, _, _ := readMetadata(task.Directory)
	if meta.Upstream == nil || meta.Upstream.Status != "changed" {
		t.Fatalf("network failure erased upstream status: %+v", meta.Upstream)
	}
}

func TestUpdateAppliesUpstreamChangesAndKeepsOldPages(t *testing.T) {
	m, task := downloaded(t)
	gallery := galleryJSON("a", "c", "d") // 第 2 页差し替え + 第 3 页新增
	var images atomic.Int32
	serveGallery(m, &gallery, &images)
	_ = m.Repair(task.Directory)
	await(t, m, 42)
	old, _ := os.ReadFile(filepath.Join(task.Directory, "0002.png"))
	if e := m.Update(task.Directory); e != nil {
		t.Fatal(e)
	}
	task = await(t, m, 42)
	if task.State != "completed" || task.Done != 3 || task.Operation != "update" {
		t.Fatalf("%+v", task)
	}
	meta, g, _ := readMetadata(task.Directory)
	if len(g.Files) != 3 || len(meta.Pages) != 3 || meta.Upstream != nil {
		t.Fatalf("%+v %+v", meta, g)
	}
	if images.Load() != 2 {
		t.Fatalf("expected only replaced and added pages to download, got %d", images.Load())
	}
	// 旧ページは削除されず superseded/ に残る。
	saved, _ := filepath.Glob(filepath.Join(task.Directory, "superseded", "*", "0002.png"))
	if len(saved) != 1 {
		t.Fatal("old page was not preserved")
	}
	if b, _ := os.ReadFile(saved[0]); !bytes.Equal(b, old) {
		t.Fatal("preserved page differs")
	}
	entries, _, _ := m.Scan()
	if len(entries) != 1 || entries[0].Upstream != "" || entries[0].Pages != 3 {
		t.Fatalf("%+v", entries)
	}
}

func TestUpdateRequiresUpstreamInformation(t *testing.T) {
	m, task := downloaded(t)
	gallery := "offline"
	var images atomic.Int32
	serveGallery(m, &gallery, &images)
	_ = m.Update(task.Directory)
	task = await(t, m, 42)
	if task.State != "failed" || images.Load() != 0 {
		t.Fatalf("%+v", task)
	}
}
