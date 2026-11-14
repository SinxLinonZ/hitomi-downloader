package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hitomi-downloader/internal/i18n"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hitomi-downloader/internal/hitomi"
)

type Page struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	SourceHash string `json:"sourceHash"`
	Format     string `json:"format"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	Fallback   string `json:"fallback,omitempty"`
}
type Metadata struct {
	Legacy        bool            `json:"-"`
	Schema        int             `json:"schemaVersion"`
	Gallery       json.RawMessage `json:"gallery"`
	Pages         []Page          `json:"pages"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	QualityPolicy string          `json:"qualityPolicy"`
	Upstream      *Upstream       `json:"upstream,omitempty"`
}
type Task struct {
	Warning   string         `json:"warning,omitempty"`
	Root      string         `json:"root"`
	ID        int            `json:"id"`
	Title     string         `json:"title"`
	State     string         `json:"state"`
	Operation string         `json:"operation"`
	Directory string         `json:"directory"`
	Done      int            `json:"done"`
	Total     int            `json:"total"`
	Errors    map[int]string `json:"errors"`
	Error     string         `json:"error,omitempty"`
	UpdatedAt time.Time      `json:"updatedAt"`
	// キューに入れた時刻。進捗では変わらないため、一覧の並び順の安定したキーになる。
	QueuedAt time.Time `json:"queuedAt,omitempty"`
}
type Entry struct {
	Tags          []Tag     `json:"tags"`
	Legacy        bool      `json:"legacy"`
	Artists       []string  `json:"artists"`
	Language      string    `json:"language"`
	Type          string    `json:"type"`
	JapaneseTitle string    `json:"japaneseTitle,omitempty"`
	Missing       int       `json:"missing"`
	Formats       []string  `json:"formats"`
	Fallbacks     int       `json:"fallbacks"`
	Upstream      string    `json:"upstream,omitempty"`
	UpstreamNote  string    `json:"upstreamNote,omitempty"`
	ID            int       `json:"id"`
	Title         string    `json:"title"`
	Directory     string    `json:"directory"`
	Pages         int       `json:"pages"`
	UpdatedAt     time.Time `json:"updatedAt"`
	// 作品ディレクトリの作成時刻（＝ダウンロード時刻）。
	CreatedAt time.Time `json:"createdAt"`
}
type Manager struct {
	mu      sync.Mutex
	tasks   map[int]*Task
	cancel  map[int]context.CancelFunc
	settled map[int]chan struct{}
	client  *hitomi.Client
	root    string
	state   string
	// 実行中のタスク数の上限（ダウンロード・検証・更新）と、移行専用の上限。
	taskLimit    *limiter
	migrateLimit *limiter
	pageWorkers  int // 1 タスク内で同時に処理するページ数。新たに開始・再開するタスクから反映される
	wg           sync.WaitGroup
	closing      bool
	library      libraryIndex
	// 直近で Hitomi への接続に失敗した時刻（検証の一括実行で無駄な待機を避ける）。
	upstreamDown time.Time
}

func AtomicJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".json-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(name, path)
}
func New(client *hitomi.Client, root, state string) (*Manager, error) {
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(root, 0755); e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(state, "tasks"), 0700); e != nil {
		return nil, e
	}
	m := &Manager{tasks: map[int]*Task{}, cancel: map[int]context.CancelFunc{}, settled: map[int]chan struct{}{}, client: client, root: root, state: state, taskLimit: newLimiter(DefaultTaskConcurrency), migrateLimit: newLimiter(migrateSlots), pageWorkers: DefaultPageConcurrency}
	files, e := os.ReadDir(filepath.Join(state, "tasks"))
	if e != nil {
		return nil, e
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, e := os.ReadFile(filepath.Join(state, "tasks", f.Name()))
		if e != nil {
			return nil, e
		}
		var t Task
		if e = json.Unmarshal(b, &t); e != nil {
			return nil, i18n.Errorf("任务文件损坏 %s：%w", f.Name(), e)
		}
		if t.ID <= 0 || f.Name() != strconv.Itoa(t.ID)+".json" {
			return nil, i18n.Errorf("无效任务文件 %s", f.Name())
		}
		if t.State == "running" || t.State == "queued" {
			t.State = "paused"
			t.Error = i18n.T("程序已重启，点击继续以恢复")
		}
		if t.Errors == nil {
			t.Errors = map[int]string{}
		}
		m.tasks[t.ID] = &t
	}
	return m, nil
}

const (
	DefaultTaskConcurrency = 2
	DefaultPageConcurrency = 4
	MaxTaskConcurrency     = 8
	MaxPageConcurrency     = 16
)

// Concurrency は現在の「同時タスク数」と「タスクあたりの同時ページ数」を返す。
func (m *Manager) Concurrency() (tasks, pages int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.taskLimit.get(), m.pageWorkers
}

// SetConcurrency は並行数を設定する。0 は「変更しない」。
// タスク数は待機中のタスクにも即座に反映され、ページ数は新たに開始・再開するタスクから反映される。
func (m *Manager) SetConcurrency(tasks, pages int) error {
	if tasks < 0 || tasks > MaxTaskConcurrency {
		return i18n.Errorf("同时任务数需在 1–%d 之间", MaxTaskConcurrency)
	}
	if pages < 0 || pages > MaxPageConcurrency {
		return i18n.Errorf("每个任务的并发文件数需在 1–%d 之间", MaxPageConcurrency)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tasks > 0 {
		m.taskLimit.setLimit(tasks)
	}
	if pages > 0 {
		m.pageWorkers = pages
	}
	return nil
}

func (m *Manager) Root() string { m.mu.Lock(); defer m.mu.Unlock(); return m.root }
func (m *Manager) SetRoot(root string) error {
	root, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(root, 0755); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.State == "running" || t.State == "queued" {
			return i18n.Errorf("请先暂停所有任务再更改下载目录")
		}
	}
	if len(m.cancel) > 0 {
		return i18n.Errorf("任务正在停止，请稍后再更改目录")
	}
	m.root = root
	m.library.mu.Lock()
	if m.library.cancel != nil {
		m.library.cancel()
	}
	m.library.generation++
	m.library.status = ScanStatus{}
	m.library.entries = nil
	m.library.byDir = nil
	m.library.mu.Unlock()
	return nil
}
func (m *Manager) save(t *Task) error {
	return AtomicJSON(filepath.Join(m.state, "tasks", strconv.Itoa(t.ID)+".json"), t)
}
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Task{}
	for _, t := range m.tasks {
		v := *t
		v.Errors = map[int]string{}
		for k, e := range t.Errors {
			v.Errors[k] = e
		}
		out = append(out, v)
	}
	sortTasks(out)
	return out
}

// 進捗で変わる UpdatedAt を主キーにすると実行中の行が毎秒入れ替わるため、
// 状態の段階 → キュー投入順 → ID の、進捗に影響されない順で並べる。
func sortTasks(tasks []Task) {
	rank := func(state string) int {
		switch state {
		case "running":
			return 0
		case "queued":
			return 1
		case "completed":
			return 3
		}
		return 2
	}
	// 旧バージョンの task には QueuedAt がないため、UpdatedAt で代用する。
	at := func(t Task) time.Time {
		if t.QueuedAt.IsZero() {
			return t.UpdatedAt
		}
		return t.QueuedAt
	}
	sort.Slice(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if ra, rb := rank(a.State), rank(b.State); ra != rb {
			return ra < rb
		}
		ta, tb := at(a), at(b)
		if !ta.Equal(tb) {
			// 実行待ちは先に入れた順、それ以外は新しい順。
			if rank(a.State) <= 1 {
				return ta.Before(tb)
			}
			return ta.After(tb)
		}
		return a.ID < b.ID
	})
}
func (m *Manager) Start(id int, dir, op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 新規ダウンロードは、すでに本地库にある作品を二重に取りに行かない（補全は検証で行う）。
	// 索引は走査の途中でもよい。見つかったものは確実に存在する。
	if op == "download" && dir == "" {
		if d, ok := existingDirs(m.indexedDirs([]int{id}, m.root))[id]; ok {
			return &AlreadyDownloadedError{ID: id, Directory: d}
		}
	}
	return m.startLocked(id, dir, op)
}

// 呼び出し元が m.mu を保持する。
func (m *Manager) startLocked(id int, dir, op string) error {
	if id <= 0 {
		return i18n.Errorf("无效 ID")
	}
	if m.closing {
		return i18n.Errorf("程序正在关闭")
	}
	if _, ok := m.cancel[id]; ok {
		return i18n.Errorf("该作品已有活动任务")
	}
	t := m.tasks[id]
	if dir != "" {
		if e := within(m.root, dir); e != nil {
			return e
		}
	}
	if t == nil || dir != "" {
		t = &Task{Root: m.root, ID: id, Directory: dir, Operation: op, Errors: map[int]string{}}
		m.tasks[id] = t
	}
	if t.Root != "" && t.Root != m.root {
		return i18n.Errorf("请切换回该任务的原下载目录再继续")
	}
	t.Root = m.root
	if t.State == "completed" && op != "repair" && op != "migrate" && op != "update" {
		return i18n.Errorf("任务已完成，请从本地库验证")
	}
	if dir != "" {
		if e := within(m.root, dir); e != nil {
			return e
		}
	}
	t.State = "queued"
	t.Error = ""
	t.Warning = ""
	t.UpdatedAt = time.Now()
	t.QueuedAt = t.UpdatedAt
	if e := m.save(t); e != nil {
		t.State = "failed"
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel[id] = cancel
	m.settled[id] = make(chan struct{})
	m.wg.Add(1)
	go m.run(ctx, id)
	return nil
}
func (m *Manager) Control(id int, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return i18n.Errorf("找不到任务")
	}
	if action == "resume" || action == "retry" {
		return m.startLocked(id, "", "")
	}
	if action != "pause" && action != "cancel" {
		return i18n.Errorf("无效操作")
	}
	if t.State == "completed" {
		return i18n.Errorf("已完成任务无法暂停或取消")
	}
	t.State = "paused"
	if action == "cancel" {
		t.State = "cancelled"
	}
	if cancel := m.cancel[id]; cancel != nil {
		cancel()
	}
	t.UpdatedAt = time.Now()
	return m.save(t)
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closing = true
	m.library.mu.Lock()
	if m.library.cancel != nil {
		m.library.cancel()
	}
	m.library.mu.Unlock()
	for id, cancel := range m.cancel {
		cancel()
		t := m.tasks[id]
		if t.State == "running" || t.State == "queued" {
			t.State = "paused"
			t.UpdatedAt = time.Now()
			if e := m.save(t); e != nil {
				log.Print(i18n.Sprintf("保存任务 #%d 的退出状态失败：%v", id, e))
			}
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}
func (m *Manager) update(id int, fn func(*Task)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	fn(t)
	t.UpdatedAt = time.Now()
	return m.save(t)
}
func (m *Manager) run(ctx context.Context, id int) {
	defer m.wg.Done()
	defer func() { m.mu.Lock(); delete(m.cancel, id); close(m.settled[id]); delete(m.settled, id); m.mu.Unlock() }()
	m.mu.Lock()
	limit := m.taskLimit
	if m.tasks[id].Operation == "migrate" {
		limit = m.migrateLimit
	}
	m.mu.Unlock()
	if e := limit.acquire(ctx); e != nil {
		return
	}
	defer limit.release()
	if ctx.Err() != nil {
		return
	}
	e := m.update(id, func(t *Task) {
		if ctx.Err() == nil {
			t.State = "running"
			t.Done = 0
			t.Errors = map[int]string{}
		}
	})
	if ctx.Err() != nil {
		e = ctx.Err()
	}
	if e == nil {
		e = m.process(ctx, id)
	}
	if e != nil {
		_ = m.update(id, func(t *Task) {
			if ctx.Err() != nil {
				if t.State == "running" || t.State == "queued" {
					t.State = "paused"
				}
			} else {
				t.State = "failed"
				t.Error = e.Error()
			}
		})
	} else {
		_ = m.update(id, func(t *Task) {
			if ctx.Err() == nil && t.State == "running" {
				t.State = "completed"
				t.Error = ""
			}
		})
	}
}
func safeTitle(s string) string {
	r := []rune(strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return '_'
		}
		return r
	}, s))
	if len(r) > 55 {
		r = r[:55]
	}
	return strings.Trim(string(r), " .")
}
func readMetadata(dir string) (Metadata, hitomi.Gallery, error) {
	var meta Metadata
	var g hitomi.Gallery
	b, e := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if e != nil {
		return meta, g, e
	}
	if e = json.Unmarshal(b, &meta); e != nil {
		return meta, g, e
	}
	raw := meta.Gallery
	if len(raw) == 0 {
		if meta.Schema != 0 {
			return meta, g, i18n.Errorf("metadata 缺少 gallery")
		}
		raw = b
		meta.Legacy = true
		meta.Gallery = raw
	} else if meta.Schema != 1 {
		return meta, g, i18n.Errorf("不支持的 metadata 版本：%d", meta.Schema)
	}
	if e = json.Unmarshal(raw, &g); e != nil {
		return meta, g, e
	}
	if g.Number() <= 0 || len(g.Files) == 0 {
		return meta, g, i18n.Errorf("无效 metadata")
	}
	g.Raw = raw
	return meta, g, nil
}
func (m *Manager) Scan() ([]Entry, []string, error) {
	out := []Entry{}
	warnings := []string{}
	e := walkLibrary(context.Background(), m.Root(), func(e Entry) { out = append(out, e) }, func(_ string, warning string) {
		if warning != "" {
			warnings = append(warnings, warning)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, warnings, e
}
func (m *Manager) Repair(dir string) error {
	if e := within(m.Root(), dir); e != nil {
		return e
	}
	meta, g, e := readMetadata(dir)
	if e != nil {
		return e
	}
	if meta.Legacy {
		return i18n.Errorf("旧 metadata 需要先手动迁移，再验证并补全")
	}
	return m.Start(g.Number(), dir, "repair")
}
func (m *Manager) process(ctx context.Context, id int) error {
	m.mu.Lock()
	task := *m.tasks[id]
	root := task.Root
	m.mu.Unlock()
	if task.Operation == "migrate" {
		return m.migrate(ctx, task)
	}
	var meta Metadata
	var g hitomi.Gallery
	var e error
	dir := task.Directory
	final := dir
	if dir != "" {
		meta, g, e = readMetadata(dir)
		if e != nil {
			return e
		}
		if meta.Legacy {
			return i18n.Errorf("旧 metadata 需要先手动迁移")
		}
		if g.Number() != id {
			return i18n.Errorf("metadata ID 不匹配")
		}
	} else {
		g, e = m.client.Gallery(ctx, id)
		if e != nil {
			return e
		}
		final = filepath.Join(root, fmt.Sprintf("%d - %s", id, safeTitle(g.Title)))
		if _, e := os.Stat(final); e == nil {
			return i18n.Errorf("目标目录已存在，请扫描本地库并使用验证补全")
		}
		dir = filepath.Join(root, fmt.Sprintf(".downloading-%d", id))
		meta, _, e = readMetadata(dir)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if meta.Schema != 0 {
			var old hitomi.Gallery
			if e = json.Unmarshal(meta.Gallery, &old); e != nil {
				return e
			}
			if old.Number() != id {
				return i18n.Errorf("临时目录 metadata ID 不匹配")
			}
		}
	}
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	cleanStaleTemp(dir)
	// 検証・更新では Hitomi の最新情報と比較し、「ローカルの破損」と「上流の更新」を区別する。
	var rs remoteState
	if task.Operation == "repair" || task.Operation == "update" {
		if rs, e = m.checkUpstream(ctx, id, g); e != nil {
			return e
		}
	}
	if task.Operation == "update" {
		if !rs.ok {
			if rs.note == "" {
				rs.note = i18n.T("未知原因")
			}
			return i18n.Errorf("无法获取 Hitomi 上的最新信息，不能应用更新：%s", rs.note)
		}
		// 差し替え・削除された旧ページは退避してから、上流の gallery を正とする。
		stamp := time.Now().Format("20060102-150405")
		kept := []Page{}
		for _, p := range meta.Pages {
			if p.Index >= len(rs.gallery.Files) || p.SourceHash != rs.gallery.Files[p.Index].Hash {
				if e = supersede(dir, p, stamp); e != nil {
					return e
				}
				continue
			}
			kept = append(kept, p)
		}
		meta.Pages = kept
		meta.Upstream = nil
		g = rs.gallery
		rs.changed = map[int]bool{}
		rs.upstream = nil
	} else if rs.upstream != nil {
		meta.Upstream = rs.upstream
	}
	if e = m.update(id, func(t *Task) {
		t.Title = g.Title
		t.Total = len(g.Files)
		if rs.note != "" {
			t.Warning = rs.note
		} else if rs.upstream != nil && rs.upstream.Status == "changed" {
			t.Warning = i18n.Sprintf("上游已更新（%s）；本地文件未被覆盖，可在“已下载”中应用上游更新", rs.upstream.Summary())
		}
	}); e != nil {
		return e
	}
	meta.Schema = 1
	meta.Gallery = g.Raw
	meta.QualityPolicy = "original-first; fallback jxl,avif,webp only when unavailable"
	pages := map[int]Page{}
	for _, p := range meta.Pages {
		if p.Index >= 0 && p.Index < len(g.Files) {
			pages[p.Index] = p
		}
	}
	var mu sync.Mutex
	var persistErr error
	save := func() error {
		meta.Pages = []Page{}
		for _, p := range pages {
			meta.Pages = append(meta.Pages, p)
		}
		sort.Slice(meta.Pages, func(i, j int) bool { return meta.Pages[i].Index < meta.Pages[j].Index })
		meta.UpdatedAt = time.Now()
		return AtomicJSON(filepath.Join(dir, "metadata.json"), meta)
	}
	if e = save(); e != nil {
		return e
	}
	if task.Directory != "" {
		m.refreshIndex(dir)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	m.mu.Lock()
	workers := m.pageWorkers
	m.mu.Unlock()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				f := g.Files[i]
				// 上流の hash が同じなら、最新の形式情報（haswebp など）を使う。
				if rs.ok && i < len(rs.gallery.Files) && rs.gallery.Files[i].Hash == f.Hash {
					f = rs.gallery.Files[i]
				}
				mu.Lock()
				p, exists := pages[i]
				mu.Unlock()
				valid := false
				if exists && p.SourceHash == f.Hash {
					if checked, e := verifyPage(dir, p); e == nil {
						valid = true
						// 移行直後のページは、初回の検証で SHA-256 基線を確定して保存する。
						if checked.SHA256 != p.SHA256 {
							mu.Lock()
							pages[i] = checked
							if e := save(); e != nil {
								persistErr = e
							}
							mu.Unlock()
						}
					}
				}
				if !valid && (rs.changed[i] || rs.deleted) {
					// 上流で差し替え・削除されたページは、旧版を再取得しない。
					reason := i18n.T("本地文件缺失或损坏，且上游已替换该页，请先应用上游更新")
					if rs.deleted {
						reason = i18n.T("本地文件缺失或损坏，但上游已删除该作品，无法补全")
					}
					if err := m.update(id, func(t *Task) { t.Errors[i+1] = reason }); err != nil {
						mu.Lock()
						persistErr = err
						mu.Unlock()
					}
					continue
				}
				if !valid {
					p, e := m.fetch(ctx, dir, i, f)
					if e != nil {
						if ctx.Err() != nil {
							return
						}
						if err := m.update(id, func(t *Task) { t.Errors[i+1] = e.Error() }); err != nil {
							mu.Lock()
							persistErr = err
							mu.Unlock()
						}
						continue
					}
					mu.Lock()
					pages[i] = p
					if e := save(); e != nil {
						persistErr = e
					}
					mu.Unlock()
				}
				if e := m.update(id, func(t *Task) { t.Done++; delete(t.Errors, i+1) }); e != nil {
					mu.Lock()
					persistErr = e
					mu.Unlock()
				}
			}
		}()
	}
send:
	for i := range g.Files {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if persistErr != nil {
		return persistErr
	}
	m.mu.Lock()
	count := len(m.tasks[id].Errors)
	done := m.tasks[id].Done
	m.mu.Unlock()
	if count > 0 || done != len(g.Files) {
		return i18n.Errorf("%d/%d 页未完成，可重试失败页面", len(g.Files)-done, len(g.Files))
	}
	if task.Directory == "" {
		if _, e = os.Stat(final); e == nil {
			return i18n.Errorf("目标目录已存在，保留临时文件")
		}
		if e = os.Rename(dir, final); e != nil {
			return e
		}
	}
	m.refreshIndex(final)
	return m.update(id, func(t *Task) { t.Directory = final })
}

// 強制終了などで残った書きかけのページ（.page-*.part）を削除する。
// 完成済みのページや metadata には触れない。放置すると完成時にそのまま作品ディレクトリへ移動してしまう。
func cleanStaleTemp(dir string) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, ".page-") && strings.HasSuffix(name, ".part") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

type byteCounter struct{ n int64 }

func (c *byteCounter) Write(b []byte) (int, error) { c.n += int64(len(b)); return len(b), nil }

func verify(dir string, p Page) error {
	_, e := verifyPage(dir, p)
	return e
}

// 基線（SHA-256）がある場合は照合し、ない場合（移行直後）は署名・デコードで検査して基線を確定する。
func verifyPage(dir string, p Page) (Page, error) {
	if filepath.Base(p.Name) != p.Name || p.Bytes <= 0 || (p.SHA256 != "" && len(p.SHA256) != 64) {
		return p, i18n.Errorf("无效页面记录")
	}
	info, e := os.Lstat(filepath.Join(dir, p.Name))
	if e != nil {
		return p, e
	}
	if !info.Mode().IsRegular() {
		return p, i18n.Errorf("页面不是普通文件")
	}
	if info.Size() != p.Bytes {
		return p, i18n.Errorf("图片校验不一致")
	}
	f, e := os.Open(filepath.Join(dir, p.Name))
	if e != nil {
		return p, e
	}
	defer f.Close()
	h := sha256.New()
	// デコーダがバッファに先読みした分も含め、実際に読み込んだ総バイト数を数える。
	counter := &byteCounter{}
	var r io.Reader = io.TeeReader(f, io.MultiWriter(h, counter))
	if p.SHA256 == "" {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(p.Name), "."))
		head := make([]byte, 512)
		n, e := io.ReadFull(f, head)
		if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
			return p, e
		}
		head = head[:n]
		if !imageSignature(head, ext) {
			return p, i18n.Errorf("图片签名与格式不符")
		}
		if _, e = f.Seek(0, 0); e != nil {
			return p, e
		}
		if ext == "png" || ext == "jpg" || ext == "jpeg" || ext == "gif" {
			if _, _, e = image.Decode(r); e != nil {
				return p, i18n.Errorf("图片解码校验失败：%w", e)
			}
		}
	}
	if _, e = io.Copy(io.Discard, r); e != nil {
		return p, e
	}
	n := counter.n
	sum := hex.EncodeToString(h.Sum(nil))
	if p.SHA256 == "" {
		// 解码で先頭側を読んだ分も Tee 経由でハッシュに入っているため、全体のハッシュになる。
		if n != p.Bytes {
			return p, i18n.Errorf("图片校验不一致")
		}
		p.SHA256 = sum
		return p, nil
	}
	if n != p.Bytes || sum != p.SHA256 {
		return p, i18n.Errorf("图片校验不一致")
	}
	return p, nil
}
func imageSignature(b []byte, ext string) bool {
	switch ext {
	case "jpg", "jpeg":
		return len(b) >= 3 && b[0] == 255 && b[1] == 216 && b[2] == 255
	case "png":
		return len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n"
	case "gif":
		return len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a")
	case "webp":
		return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	case "avif":
		return len(b) >= 16 && string(b[4:8]) == "ftyp" && (strings.Contains(string(b[8:]), "avif") || strings.Contains(string(b[8:]), "avis"))
	case "jxl":
		return len(b) >= 2 && b[0] == 255 && b[1] == 10 || len(b) >= 12 && string(b[4:8]) == "JXL "
	}
	return false
}
func (m *Manager) fetch(ctx context.Context, dir string, index int, f hitomi.File) (Page, error) {
	formats := hitomi.Formats(f)
	if len(formats) == 0 {
		return Page{}, i18n.Errorf("没有可用图片格式")
	}
	reason := ""
	for _, ext := range formats {
		for refresh := 0; refresh < 2; refresh++ {
			u, e := m.client.URL(ctx, f, ext, refresh == 1)
			if e != nil {
				return Page{}, e
			}
			resp, e := m.client.Image(ctx, u)
			if e != nil {
				var he *hitomi.Error
				// 形式ごとのホストが存在しない場合も、その形式は提供されていないとみなして次へ回退する。
				if errors.As(e, &he) && he.Code == "UPSTREAM_DNS" {
					reason = i18n.Sprintf("%s 不可用 (域名无法解析)", ext)
					break
				}
				if errors.As(e, &he) && (he.Status == 404 || he.Status == 403) {
					if refresh == 0 {
						continue
					}
					reason = i18n.Sprintf("%s 不可用 (HTTP %d)", ext, he.Status)
					break
				}
				return Page{}, e
			}
			p, e := writeImage(resp, dir, index, f, ext)
			if e != nil {
				return Page{}, e
			}
			p.Fallback = reason
			return p, nil
		}
	}
	return Page{}, i18n.Errorf("源文件及可用衍生格式均不可用：%s", reason)
}
func writeImage(resp *http.Response, dir string, index int, f hitomi.File, ext string) (Page, error) {
	defer resp.Body.Close()
	var p Page
	tmp, e := os.CreateTemp(dir, ".page-*.part")
	if e != nil {
		return p, e
	}
	name := tmp.Name()
	defer os.Remove(name)
	h := sha256.New()
	head := make([]byte, 512)
	n, e := io.ReadFull(resp.Body, head)
	if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
		tmp.Close()
		return p, e
	}
	head = head[:n]
	if !imageSignature(head, ext) {
		tmp.Close()
		return p, i18n.Errorf("上游返回了无效 %s 图片", ext)
	}
	count, e := io.Copy(io.MultiWriter(tmp, h), io.MultiReader(strings.NewReader(string(head)), io.LimitReader(resp.Body, 256<<20)))
	if e == nil && count >= 256<<20 {
		e = i18n.Errorf("图片超出大小限制")
	}
	if e == nil && resp.ContentLength >= 0 && resp.ContentLength != count {
		e = i18n.Errorf("图片内容被截断")
	}
	if e == nil {
		e = tmp.Sync()
	}
	ce := tmp.Close()
	if e != nil {
		return p, e
	}
	if ce != nil {
		return p, ce
	}
	if ext == "jpg" || ext == "jpeg" || ext == "png" || ext == "gif" {
		check, e := os.Open(name)
		if e != nil {
			return p, e
		}
		cfg, _, e := image.DecodeConfig(check)
		if e == nil && (cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 100_000_000) {
			e = i18n.Errorf("图片尺寸超过限制")
		}
		if e == nil {
			_, e = check.Seek(0, 0)
		}
		if e == nil {
			_, _, e = image.Decode(check)
		}
		check.Close()
		if e != nil {
			return p, i18n.Errorf("图片解码校验失败：%w", e)
		}
	}
	local := fmt.Sprintf("%04d.%s", index+1, ext)
	if e = os.Rename(name, filepath.Join(dir, local)); e != nil {
		return p, e
	}
	return Page{index, local, f.Hash, ext, count, hex.EncodeToString(h.Sum(nil)), ""}, nil
}

func within(root, path string) error {
	realRoot, e := filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	realPath, e := filepath.EvalSymlinks(path)
	if e != nil {
		return e
	}
	rel, e := filepath.Rel(realRoot, realPath)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return i18n.Errorf("目录不在当前下载目录中")
	}
	return nil
}
