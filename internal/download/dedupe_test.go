package download

import (
	"errors"
	"os"
	"testing"
	"time"
)

// 本地库にある作品は新規ダウンロードを受け付けず、既存ファイルにも触れない。
func TestStartDownloadRejectsWorkAlreadyInLibrary(t *testing.T) {
	m, _, _ := fixture(t)
	dir, raw := legacyFixture(t, m, 123, 1)
	indexReady(t, m)
	e := m.Start(123, "", "download")
	var dup *AlreadyDownloadedError
	if !errors.As(e, &dup) || dup.ID != 123 || dup.Directory != dir {
		t.Fatalf("got %v", e)
	}
	if len(m.List()) != 0 {
		t.Fatal("rejected download must not create a task")
	}
	if after, _ := os.ReadFile(dir + "/metadata.json"); string(after) != string(raw) {
		t.Fatal("metadata changed")
	}
	// 検証・補全などの操作は従来どおり受け付ける。
	if e := m.Start(123, "", "repair"); errors.As(e, &dup) {
		t.Fatalf("repair must not be rejected: %v", e)
	}
}

// 索引が古くなって実体が消えている場合は、ダウンロードを止めない。
func TestStartDownloadIgnoresStaleIndexEntry(t *testing.T) {
	m, _, _ := fixture(t)
	dir, _ := legacyFixture(t, m, 123, 1)
	indexReady(t, m)
	if e := os.RemoveAll(dir); e != nil {
		t.Fatal(e)
	}
	if got := m.DownloadedDirs([]int{123}); len(got) != 0 {
		t.Fatalf("stale entry reported as downloaded: %v", got)
	}
}

func TestDownloadedDirsReportsOnlyKnownWorks(t *testing.T) {
	m, _, _ := fixture(t)
	legacyFixture(t, m, 123, 1)
	legacyFixture(t, m, 124, 1)
	indexReady(t, m)
	got := m.DownloadedDirs([]int{123, 999})
	if len(got) != 1 || got[123] == "" {
		t.Fatalf("%v", got)
	}
}

// 実行中の進捗で UpdatedAt が変わっても、一覧の並び順は変わらない。
func TestTaskOrderIsStableWhileProgressUpdates(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	tasks := func(bump int) []Task {
		return []Task{
			{ID: 3, State: "running", QueuedAt: base.Add(3 * time.Second), UpdatedAt: base.Add(time.Duration(bump) * time.Second)},
			{ID: 1, State: "running", QueuedAt: base.Add(1 * time.Second), UpdatedAt: base.Add(time.Duration(bump+1) * time.Second)},
			{ID: 2, State: "running", QueuedAt: base.Add(2 * time.Second), UpdatedAt: base.Add(time.Duration(bump+2) * time.Second)},
			{ID: 9, State: "queued", QueuedAt: base, UpdatedAt: base},
			{ID: 5, State: "completed", UpdatedAt: base.Add(10 * time.Second)},
			{ID: 6, State: "failed", UpdatedAt: base.Add(20 * time.Second)},
		}
	}
	order := func(ts []Task) []int {
		sortTasks(ts)
		ids := []int{}
		for _, task := range ts {
			ids = append(ids, task.ID)
		}
		return ids
	}
	want := []int{1, 2, 3, 9, 6, 5}
	for _, bump := range []int{0, 7, 50} {
		got := order(tasks(bump))
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("bump %d: got %v want %v", bump, got, want)
			}
		}
	}
}

func TestLibrarySortByDownloadTime(t *testing.T) {
	m, _, _ := fixture(t)
	a, _ := legacyFixture(t, m, 1, 1)
	b, _ := legacyFixture(t, m, 2, 1)
	indexReady(t, m)
	// 作成時刻を直接は変えられないので、索引上の値を差し替えて並び替えだけを確かめる。
	m.library.mu.Lock()
	base := time.Now()
	for i := range m.library.entries {
		switch m.library.entries[i].Directory {
		case a:
			m.library.entries[i].CreatedAt = base.Add(-time.Hour)
			m.library.entries[i].UpdatedAt = base
		case b:
			m.library.entries[i].CreatedAt = base
			m.library.entries[i].UpdatedAt = base.Add(-time.Hour)
		}
	}
	m.library.mu.Unlock()
	ids := func(sortBy string) [2]int {
		page, e := m.LibrarySorted(1, 20, "", "all", sortBy)
		if e != nil || len(page.Entries) != 2 {
			t.Fatalf("%v %+v", e, page)
		}
		return [2]int{page.Entries[0].ID, page.Entries[1].ID}
	}
	if got := ids("created"); got != [2]int{2, 1} {
		t.Fatalf("created: %v", got)
	}
	if got := ids("created-asc"); got != [2]int{1, 2} {
		t.Fatalf("created-asc: %v", got)
	}
	if got := ids(""); got != [2]int{1, 2} {
		t.Fatalf("default (updated): %v", got)
	}
}

func TestScanRecordsDirectoryCreationTime(t *testing.T) {
	m, _, _ := fixture(t)
	legacyFixture(t, m, 1, 1)
	page := indexReady(t, m)
	if len(page.Entries) != 1 || page.Entries[0].CreatedAt.IsZero() {
		t.Fatalf("%+v", page.Entries)
	}
	if d := time.Since(page.Entries[0].CreatedAt); d < -time.Minute || d > time.Hour {
		t.Fatalf("unexpected creation time %v", page.Entries[0].CreatedAt)
	}
}
