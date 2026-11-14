package download

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func partFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, ".page-*.part"))
	return files
}

// 暂停时已完成的页面必须保留，继续时只取剩余页面，且不残留临时文件。
func TestPauseKeepsFinishedPagesAndResumeFetchesOnlyRemaining(t *testing.T) {
	m, count, mu := fixture(t)
	old := m.client.HTTP.Transport
	var blockSecond atomic.Bool
	blockSecond.Store(true)
	entered := make(chan struct{}, 1)
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		if blockSecond.Load() && strings.Contains(r.URL.Path, strings.Repeat("b", 64)) {
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
		t.Fatal("second page was not requested")
	}
	// 第 1 页完成并记录进度后再暂停。
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		done := m.tasks[42].Done
		m.mu.Unlock()
		if done == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first page did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e := m.Control(42, "pause"); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 42)
	partial := filepath.Join(m.Root(), ".downloading-42")
	if task.State != "paused" || task.Errors[2] != "" {
		t.Fatalf("pause must not record the interrupted page as an error: %+v", task)
	}
	meta, _, e := readMetadata(partial)
	if e != nil || len(meta.Pages) != 1 || meta.Pages[0].Index != 0 || verify(partial, meta.Pages[0]) != nil {
		t.Fatalf("finished page was lost: %+v %v", meta.Pages, e)
	}
	if len(partFiles(t, partial)) != 0 {
		t.Fatal("pause left a partial page file")
	}
	mu.Lock()
	before := *count
	mu.Unlock()

	blockSecond.Store(false)
	if e := m.Control(42, "resume"); e != nil {
		t.Fatal(e)
	}
	task = await(t, m, 42)
	mu.Lock()
	after := *count
	mu.Unlock()
	if task.State != "completed" || task.Done != 2 || after-before != 1 {
		t.Fatalf("resume must fetch only the remaining page: %+v (%d -> %d)", task, before, after)
	}
	if _, e := os.Stat(partial); !os.IsNotExist(e) {
		t.Fatal("temporary directory remained after completion")
	}
	if len(partFiles(t, task.Directory)) != 0 {
		t.Fatal("part files reached the final directory")
	}
}

// 取消不会删除已下载的内容，之后仍可继续。
func TestCancelKeepsPartialDataAndCanResume(t *testing.T) {
	m, _, _ := fixture(t)
	old := m.client.HTTP.Transport
	var block atomic.Bool
	block.Store(true)
	entered := make(chan struct{}, 2)
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		if block.Load() && strings.Contains(r.URL.Path, strings.Repeat("b", 64)) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return old.RoundTrip(r)
	})
	_ = m.Start(42, "", "download")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	time.Sleep(100 * time.Millisecond)
	_ = m.Control(42, "cancel")
	task := await(t, m, 42)
	if task.State != "cancelled" {
		t.Fatalf("%+v", task)
	}
	if _, e := os.Stat(filepath.Join(m.Root(), ".downloading-42", "metadata.json")); e != nil {
		t.Fatal("cancel removed partial data")
	}
	block.Store(false)
	if e := m.Control(42, "resume"); e != nil {
		t.Fatal(e)
	}
	if task = await(t, m, 42); task.State != "completed" || task.Done != 2 {
		t.Fatalf("%+v", task)
	}
}

// 强制终止后遗留的书きかけページは、再開時に削除され、完成した作品に混入しない。
func TestStalePartFilesAreRemovedBeforeResume(t *testing.T) {
	m, _, _ := fixture(t)
	partial := filepath.Join(m.Root(), ".downloading-42")
	_ = os.MkdirAll(partial, 0755)
	stale := filepath.Join(partial, ".page-12345.part")
	if e := os.WriteFile(stale, []byte("half written"), 0600); e != nil {
		t.Fatal(e)
	}
	_ = m.Start(42, "", "download")
	task := await(t, m, 42)
	if task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	if len(partFiles(t, task.Directory)) != 0 {
		t.Fatal("stale part file moved into the final directory")
	}
}
