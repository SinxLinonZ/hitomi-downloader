package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func legacyFixture(t *testing.T, m *Manager, id, pages int) (string, []byte) {
	t.Helper()
	dir := filepath.Join(m.Root(), fmt.Sprintf("legacy-%d", id))
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	files := []map[string]any{}
	for i := 0; i < pages; i++ {
		files = append(files, map[string]any{"name": "source.jpg", "hash": strings.Repeat("a", 64), "haswebp": 1, "width": 3, "height": 3})
		if e := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d.png", i+1)), testPNG(), 0600); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := json.Marshal(map[string]any{"id": id, "title": fmt.Sprintf("Legacy %d", id), "japaneseTitle": "日本語タイトル", "language": "japanese", "type": "manga", "artists": []string{"Some Artist"}, "tags": []map[string]any{{"tag": "digital"}}, "files": files})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "metadata.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	return dir, raw
}
func indexReady(t *testing.T, m *Manager) LibraryPage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		page, e := m.Library(1, 20, "", "all")
		if e != nil {
			t.Fatal(e)
		}
		if page.Scan.State != "running" {
			return page
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("scan did not finish")
	return LibraryPage{}
}

func TestLegacyScanDoesNotChangeFilesAndRequiresMigration(t *testing.T) {
	m, _, _ := fixture(t)
	dir, raw := legacyFixture(t, m, 123, 2)
	page := indexReady(t, m)
	if page.LegacyTotal != 1 || len(page.Entries) != 1 || !page.Entries[0].Legacy || page.Entries[0].Artists[0] != "Some Artist" {
		t.Fatalf("%+v", page)
	}
	if e := m.Repair(dir); e == nil {
		t.Fatal("legacy repair should require explicit migration")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if !bytes.Equal(after, raw) {
		t.Fatal("scan or repair rewrote legacy metadata")
	}
	if _, e := os.Stat(filepath.Join(dir, "metadata.legacy.json")); !os.IsNotExist(e) {
		t.Fatal("scan created backup")
	}
}

func TestMigratePreservesImagesAndCreatesOfflineManifest(t *testing.T) {
	m, _, _ := fixture(t)
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		t.Errorf("migration accessed network: %s", r.URL)
		return nil, fmt.Errorf("offline")
	})
	dir, raw := legacyFixture(t, m, 123, 2)
	before, _ := os.ReadFile(filepath.Join(dir, "0001.png"))
	_ = indexReady(t, m)
	if e := m.Migrate(dir); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 123)
	if task.State != "completed" || task.Warning != "" || task.Done != 2 {
		t.Fatalf("%+v", task)
	}
	backup, _ := os.ReadFile(filepath.Join(dir, "metadata.legacy.json"))
	if !bytes.Equal(backup, raw) {
		t.Fatal("original metadata was not backed up")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "0001.png"))
	if !bytes.Equal(after, before) {
		t.Fatal("migration altered image")
	}
	meta, g, e := readMetadata(dir)
	if e != nil || meta.Legacy || len(meta.Pages) != 2 || g.JapaneseTitle != "日本語タイトル" {
		t.Fatalf("%+v %+v %v", meta, g, e)
	}
	for _, p := range meta.Pages {
		if p.SHA256 != "" {
			t.Fatal("migration should defer the checksum baseline")
		}
		if e := verify(dir, p); e != nil {
			t.Fatal(e)
		}
	}
	page, e := m.Library(1, 20, "artist", "native")
	if e != nil || page.Total != 1 || page.LegacyTotal != 0 {
		t.Fatalf("%+v %v", page, e)
	}
	if e = m.Migrate(dir); e == nil {
		t.Fatal("repeated migration accepted")
	}
	// 验证会尝试联网对比上游；离线时仍应完成本地校验，并提示无法判断上游状态。
	m.client.Backoff = time.Millisecond
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) { return nil, fmt.Errorf("offline") })
	if e = m.Repair(dir); e != nil {
		t.Fatal(e)
	}
	if task = await(t, m, 123); task.State != "completed" || !strings.Contains(task.Warning, "仅完成本地校验") {
		t.Fatalf("offline verification failed: %+v", task)
	}
	// 初回の検証で基線が確定し、以降は内容の変化を検出できる。
	meta, _, e = readMetadata(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range meta.Pages {
		if len(p.SHA256) != 64 {
			t.Fatalf("baseline not recorded: %+v", p)
		}
	}
	if e = verify(dir, meta.Pages[0]); e != nil {
		t.Fatal(e)
	}
	bad := meta.Pages[0]
	bad.SHA256 = strings.Repeat("0", 64)
	if e = verify(dir, bad); e == nil {
		t.Fatal("changed checksum accepted")
	}
}

func TestMigrateRecordsMissingAndCorruptPagesWithoutDownloading(t *testing.T) {
	m, _, _ := fixture(t)
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		t.Errorf("migration accessed network")
		return nil, fmt.Errorf("offline")
	})
	dir, _ := legacyFixture(t, m, 123, 3)
	_ = os.Remove(filepath.Join(dir, "0002.png"))
	_ = os.WriteFile(filepath.Join(dir, "0003.png"), []byte("bad PNG"), 0600)
	if e := m.Migrate(dir); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 123)
	if task.State != "completed" || task.Warning == "" || len(task.Errors) != 2 {
		t.Fatalf("%+v", task)
	}
	meta, _, e := readMetadata(dir)
	if e != nil || len(meta.Pages) != 1 {
		t.Fatalf("%+v %v", meta, e)
	}
	page := indexReady(t, m)
	if len(page.Entries) != 1 || page.Entries[0].Missing != 2 {
		t.Fatalf("%+v", page)
	}
}

func TestMigrationCancellationLeavesLegacyMetadataUntouched(t *testing.T) {
	m, _, _ := fixture(t)
	dir, raw := legacyFixture(t, m, 123, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.tasks[123] = &Task{ID: 123, Root: m.Root(), Directory: dir, Errors: map[int]string{}}
	if e := m.migrate(ctx, *m.tasks[123]); e == nil {
		t.Fatal("cancelled migration succeeded")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if !bytes.Equal(raw, after) {
		t.Fatal("cancelled migration changed metadata")
	}
	if _, e := os.Stat(filepath.Join(dir, "metadata.legacy.json")); !os.IsNotExist(e) {
		t.Fatal("cancelled migration created backup")
	}
}

func TestMigrationDoesNotOverwriteDifferentBackup(t *testing.T) {
	m, _, _ := fixture(t)
	dir, raw := legacyFixture(t, m, 123, 1)
	_ = os.WriteFile(filepath.Join(dir, "metadata.legacy.json"), []byte("other backup"), 0600)
	if e := m.Migrate(dir); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 123)
	if task.State != "failed" {
		t.Fatalf("%+v", task)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if !bytes.Equal(raw, after) {
		t.Fatal("failed migration changed metadata")
	}
	backup, _ := os.ReadFile(filepath.Join(dir, "metadata.legacy.json"))
	if string(backup) != "other backup" {
		t.Fatal("overwrote existing backup")
	}
}

func TestScanSkipsArtworkChildrenAndPaginatesCachedResults(t *testing.T) {
	m, _, _ := fixture(t)
	for i := 0; i < 25; i++ {
		dir, _ := legacyFixture(t, m, 100+i, 1)
		nested := filepath.Join(dir, "images", "nested")
		_ = os.MkdirAll(nested, 0755)
		_ = os.WriteFile(filepath.Join(nested, "metadata.json"), []byte("invalid nested metadata"), 0600)
	}
	page := indexReady(t, m)
	if page.Scan.WarningCount != 0 || page.Total != 25 || len(page.Entries) != 20 {
		t.Fatalf("%+v", page)
	}
	second, e := m.Library(2, 20, "", "legacy")
	if e != nil || len(second.Entries) != 5 {
		t.Fatalf("%+v %v", second, e)
	}
	dir := page.Entries[0].Directory
	if e = os.Remove(filepath.Join(dir, "metadata.json")); e != nil {
		t.Fatal(e)
	}
	cached, e := m.Library(1, 20, "", "all")
	if e != nil || cached.Total != 25 {
		t.Fatal("cached query unexpectedly rescanned")
	}
	if e = m.StartScan(true); e != nil {
		t.Fatal(e)
	}
	rescanned := indexReady(t, m)
	if rescanned.Total != 24 {
		t.Fatalf("rescan did not refresh cache: %+v", rescanned)
	}
}

func TestWalkLibraryCancellationAndLocalCover(t *testing.T) {
	m, _, _ := fixture(t)
	dir, _ := legacyFixture(t, m, 123, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := walkLibrary(ctx, m.Root(), func(Entry) { t.Fatal("cancelled scan emitted result") }, func(string, string) {}); e != context.Canceled {
		t.Fatal(e)
	}
	path, e := m.LocalCover(dir)
	if e != nil || filepath.Base(path) != "0001.png" {
		t.Fatalf("%s %v", path, e)
	}
	if _, e = m.LocalCover(t.TempDir()); e == nil {
		t.Fatal("cover escaped root")
	}
}

func TestResumeMigrationAfterMetadataWasCommitted(t *testing.T) {
	m, _, _ := fixture(t)
	dir, _ := legacyFixture(t, m, 123, 1)
	if e := m.Migrate(dir); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 123)
	if task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	m.mu.Lock()
	m.tasks[123].State = "paused"
	m.mu.Unlock()
	if e := m.Control(123, "resume"); e != nil {
		t.Fatal(e)
	}
	task = await(t, m, 123)
	if task.State != "completed" || task.Done != 1 {
		t.Fatalf("%+v", task)
	}
}

func TestMigrateRejectsChangedGalleryID(t *testing.T) {
	m, _, _ := fixture(t)
	dir, raw := legacyFixture(t, m, 123, 1)
	if e := m.migrate(context.Background(), Task{ID: 999, Root: m.Root(), Directory: dir}); e == nil {
		t.Fatal("accepted metadata from a different gallery")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if !bytes.Equal(raw, after) {
		t.Fatal("changed metadata on ID mismatch")
	}
}

func TestMigrationTargetsCaptureAllPagesAndRemainStable(t *testing.T) {
	m, _, _ := fixture(t)
	for i := 0; i < 205; i++ {
		legacyFixture(t, m, 1000+i, 1)
	}
	if _, e := m.MigrationTargets(); e == nil {
		t.Fatal("accepted targets before scan completed")
	}
	_ = indexReady(t, m)
	targets, e := m.MigrationTargets()
	if e != nil || len(targets.Directories) != 205 || targets.Skipped != 0 {
		t.Fatalf("%+v %v", targets, e)
	}
	if e = m.Migrate(targets.Directories[0]); e != nil {
		t.Fatal(e)
	}
	meta, _, e := readMetadata(targets.Directories[0])
	if e != nil {
		t.Fatal(e)
	}
	var legacy struct {
		ID int `json:"id"`
	}
	if e = json.Unmarshal(meta.Gallery, &legacy); e != nil {
		t.Fatal(e)
	}
	if task := await(t, m, legacy.ID); task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	remaining, e := m.MigrationTargets()
	if e != nil || len(remaining.Directories) != 204 {
		t.Fatalf("remaining=%d %v", len(remaining.Directories), e)
	}
	if len(targets.Directories) != 205 {
		t.Fatal("migration changed captured target list")
	}
	seen := map[string]bool{}
	for _, dir := range remaining.Directories {
		if seen[dir] || dir == targets.Directories[0] {
			t.Fatal("duplicate or already migrated target", dir)
		}
		seen[dir] = true
	}
}

func TestMigrationTargetsSkipActiveTasksAndRejectStaleRoot(t *testing.T) {
	m, _, _ := fixture(t)
	first, _ := legacyFixture(t, m, 123, 1)
	second, _ := legacyFixture(t, m, 124, 1)
	_ = indexReady(t, m)
	_ = m.taskLimit.acquire(context.Background())
	_ = m.taskLimit.acquire(context.Background())
	if e := m.Migrate(first); e != nil {
		t.Fatal(e)
	}
	targets, e := m.MigrationTargets()
	if e != nil || targets.Skipped != 1 || len(targets.Directories) != 1 || targets.Directories[0] != second {
		t.Fatalf("%+v %v", targets, e)
	}
	if e = m.Control(123, "pause"); e != nil {
		t.Fatal(e)
	}
	_ = await(t, m, 123)
	if e = m.SetRoot(t.TempDir()); e != nil {
		t.Fatal(e)
	}
	if _, e = m.MigrationTargets(); e == nil {
		t.Fatal("accepted targets from previous download root")
	}
}
