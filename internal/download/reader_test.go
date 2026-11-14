package download

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReaderServesPagesByIDWithoutExposingPaths(t *testing.T) {
	m, task := downloaded(t)
	_ = indexReady(t, m)
	work, e := m.ReaderWork(42)
	if e != nil || len(work.List) != 2 || work.Title != "Fixture" {
		t.Fatalf("%+v %v", work, e)
	}
	path, e := m.ReaderPageFile(42, 1)
	if e != nil || filepath.Dir(path) != task.Directory {
		t.Fatalf("%s %v", path, e)
	}
	for _, n := range []int{0, 3, -1} {
		if _, e := m.ReaderPageFile(42, n); !errors.Is(e, os.ErrNotExist) {
			t.Fatalf("page %d accepted: %v", n, e)
		}
	}
	if _, e := m.ReaderWork(999); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("unknown work: %v", e)
	}
	page, e := m.ReaderLibrary(1, 20, "", "")
	if e != nil || len(page.Entries) != 1 {
		t.Fatalf("%+v %v", page, e)
	}
	b, _ := json.Marshal(page)
	if strings.Contains(string(b), m.Root()) || strings.Contains(string(b), "directory") {
		t.Fatalf("reader response leaks filesystem paths: %s", b)
	}
}

func TestReaderIgnoresRecordedNamesOutsideTheWorkDirectory(t *testing.T) {
	m, task := downloaded(t)
	meta, _, _ := readMetadata(task.Directory)
	meta.Pages[0].Name = "../escape.png"
	if e := AtomicJSON(filepath.Join(task.Directory, "metadata.json"), meta); e != nil {
		t.Fatal(e)
	}
	_ = indexReady(t, m)
	// 越界の記録名は無視され、作品ディレクトリ内の通常ファイル以外は返さない。
	path, e := m.ReaderPageFile(42, 1)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if e == nil && filepath.Dir(path) != task.Directory {
		t.Fatalf("page outside the directory was served: %s", path)
	}
	if _, e := m.ReaderPageFile(42, 2); e != nil {
		t.Fatal(e)
	}
}

func TestReaderSupportsLegacyDirectoriesAndMissingPages(t *testing.T) {
	m, _, _ := fixture(t)
	dir, _ := legacyFixture(t, m, 123, 3)
	_ = os.Remove(filepath.Join(dir, "0002.png"))
	_ = indexReady(t, m)
	work, e := m.ReaderWork(123)
	if e != nil || len(work.List) != 3 {
		t.Fatalf("%+v %v", work, e)
	}
	if _, e := m.ReaderPageFile(123, 1); e != nil {
		t.Fatal(e)
	}
	if _, e := m.ReaderPageFile(123, 2); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("missing page was served")
	}
	if cover, e := m.ReaderCoverFile(123); e != nil || filepath.Base(cover) != "0001.png" {
		t.Fatalf("%s %v", cover, e)
	}
}

func TestReaderSeesMetadataChangesAfterUpdate(t *testing.T) {
	m, task := downloaded(t)
	_ = indexReady(t, m)
	if _, e := m.ReaderPageFile(42, 2); e != nil {
		t.Fatal(e)
	}
	// metadata が更新されたらキャッシュを使わず再読込する（mtime が変わる）。
	meta, _, _ := readMetadata(task.Directory)
	meta.Pages = meta.Pages[:1]
	path := filepath.Join(task.Directory, "metadata.json")
	if e := AtomicJSON(path, meta); e != nil {
		t.Fatal(e)
	}
	future := meta.UpdatedAt.AddDate(0, 0, 1)
	_ = os.Chtimes(path, future, future)
	_ = os.Remove(filepath.Join(task.Directory, "0002.png"))
	if _, e := m.ReaderPageFile(42, 2); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("stale cache served a removed page: %v", e)
	}
}

func TestReaderLibrarySortsByDownloadTime(t *testing.T) {
	m, _, _ := fixture(t)
	a, _ := legacyFixture(t, m, 1, 1)
	legacyFixture(t, m, 2, 1)
	indexReady(t, m)
	m.library.mu.Lock()
	base := time.Now()
	for i := range m.library.entries {
		if m.library.entries[i].Directory == a {
			m.library.entries[i].CreatedAt = base
			m.library.entries[i].UpdatedAt = base.Add(-time.Hour)
		} else {
			m.library.entries[i].CreatedAt = base.Add(-time.Hour)
			m.library.entries[i].UpdatedAt = base
		}
	}
	m.library.mu.Unlock()
	first := func(sortBy string) int {
		page, e := m.ReaderLibrary(1, 20, "", sortBy)
		if e != nil || len(page.Entries) != 2 {
			t.Fatalf("%v %+v", e, page)
		}
		return page.Entries[0].ID
	}
	if first("created") != 1 || first("created-asc") != 2 || first("") != 2 {
		t.Fatal("reader library ignores sort")
	}
}
