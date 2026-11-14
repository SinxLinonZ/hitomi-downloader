package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	ID            int       `json:"id"`
	Title         string    `json:"title"`
	Directory     string    `json:"directory"`
	Pages         int       `json:"pages"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
type Manager struct {
	mu      sync.Mutex
	tasks   map[int]*Task
	cancel  map[int]context.CancelFunc
	settled map[int]chan struct{}
	client  *hitomi.Client
	root    string
	state   string
	slots   chan struct{}
	wg      sync.WaitGroup
	closing bool
	library libraryIndex
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
	m := &Manager{tasks: map[int]*Task{}, cancel: map[int]context.CancelFunc{}, settled: map[int]chan struct{}{}, client: client, root: root, state: state, slots: make(chan struct{}, 2)}
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
			return nil, fmt.Errorf("任务文件损坏 %s：%w", f.Name(), e)
		}
		if t.ID <= 0 || f.Name() != strconv.Itoa(t.ID)+".json" {
			return nil, fmt.Errorf("无效任务文件 %s", f.Name())
		}
		if t.State == "running" || t.State == "queued" {
			t.State = "paused"
			t.Error = "程序已重启，点击继续以恢复"
		}
		if t.Errors == nil {
			t.Errors = map[int]string{}
		}
		m.tasks[t.ID] = &t
	}
	return m, nil
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
			return fmt.Errorf("请先暂停所有任务再更改下载目录")
		}
	}
	if len(m.cancel) > 0 {
		return fmt.Errorf("任务正在停止，请稍后再更改目录")
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
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}
func (m *Manager) Start(id int, dir, op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(id, dir, op)
}

// 呼び出し元が m.mu を保持する。
func (m *Manager) startLocked(id int, dir, op string) error {
	if id <= 0 {
		return fmt.Errorf("无效 ID")
	}
	if m.closing {
		return fmt.Errorf("程序正在关闭")
	}
	if _, ok := m.cancel[id]; ok {
		return fmt.Errorf("该作品已有活动任务")
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
		return fmt.Errorf("请切换回该任务的原下载目录再继续")
	}
	t.Root = m.root
	if t.State == "completed" && op != "repair" && op != "migrate" {
		return fmt.Errorf("任务已完成，请从本地库验证")
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
		return fmt.Errorf("找不到任务")
	}
	if action == "resume" || action == "retry" {
		return m.startLocked(id, "", "")
	}
	if action != "pause" && action != "cancel" {
		return fmt.Errorf("无效操作")
	}
	if t.State == "completed" {
		return fmt.Errorf("已完成任务无法暂停或取消")
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
				log.Printf("保存任务 #%d 的退出状态失败：%v", id, e)
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
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		return
	}
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
			return meta, g, fmt.Errorf("metadata 缺少 gallery")
		}
		raw = b
		meta.Legacy = true
		meta.Gallery = raw
	} else if meta.Schema != 1 {
		return meta, g, fmt.Errorf("不支持的 metadata 版本：%d", meta.Schema)
	}
	if e = json.Unmarshal(raw, &g); e != nil {
		return meta, g, e
	}
	if g.Number() <= 0 || len(g.Files) == 0 {
		return meta, g, fmt.Errorf("无效 metadata")
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
		return fmt.Errorf("旧 metadata 需要先手动迁移，再验证并补全")
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
			return fmt.Errorf("旧 metadata 需要先手动迁移")
		}
		if g.Number() != id {
			return fmt.Errorf("metadata ID 不匹配")
		}
	} else {
		g, e = m.client.Gallery(ctx, id)
		if e != nil {
			return e
		}
		final = filepath.Join(root, fmt.Sprintf("%d - %s", id, safeTitle(g.Title)))
		if _, e := os.Stat(final); e == nil {
			return fmt.Errorf("目标目录已存在，请扫描本地库并使用验证补全")
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
				return fmt.Errorf("临时目录 metadata ID 不匹配")
			}
		}
	}
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	if e = m.update(id, func(t *Task) { t.Title = g.Title; t.Total = len(g.Files) }); e != nil {
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
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				f := g.Files[i]
				mu.Lock()
				p, exists := pages[i]
				mu.Unlock()
				valid := false
				if exists && p.SourceHash == f.Hash {
					valid = verify(dir, p) == nil
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
		return fmt.Errorf("%d/%d 页未完成，可重试失败页面", len(g.Files)-done, len(g.Files))
	}
	if task.Directory == "" {
		if _, e = os.Stat(final); e == nil {
			return fmt.Errorf("目标目录已存在，保留临时文件")
		}
		if e = os.Rename(dir, final); e != nil {
			return e
		}
	}
	m.refreshIndex(final)
	return m.update(id, func(t *Task) { t.Directory = final })
}
func verify(dir string, p Page) error {
	if filepath.Base(p.Name) != p.Name || p.Bytes <= 0 || len(p.SHA256) != 64 {
		return fmt.Errorf("无效页面记录")
	}
	info, e := os.Lstat(filepath.Join(dir, p.Name))
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("页面不是普通文件")
	}
	f, e := os.Open(filepath.Join(dir, p.Name))
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil {
		return e
	}
	if n != p.Bytes || hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
		return fmt.Errorf("图片校验不一致")
	}
	return nil
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
		return Page{}, fmt.Errorf("没有可用图片格式")
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
				if errors.As(e, &he) && (he.Status == 404 || he.Status == 403) {
					if refresh == 0 {
						continue
					}
					reason = fmt.Sprintf("%s 不可用 (HTTP %d)", ext, he.Status)
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
	return Page{}, fmt.Errorf("源文件及可用衍生格式均不可用：%s", reason)
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
		return p, fmt.Errorf("上游返回了无效 %s 图片", ext)
	}
	count, e := io.Copy(io.MultiWriter(tmp, h), io.MultiReader(strings.NewReader(string(head)), io.LimitReader(resp.Body, 256<<20)))
	if e == nil && count >= 256<<20 {
		e = fmt.Errorf("图片超出大小限制")
	}
	if e == nil && resp.ContentLength >= 0 && resp.ContentLength != count {
		e = fmt.Errorf("图片内容被截断")
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
			e = fmt.Errorf("图片尺寸超过限制")
		}
		if e == nil {
			_, e = check.Seek(0, 0)
		}
		if e == nil {
			_, _, e = image.Decode(check)
		}
		check.Close()
		if e != nil {
			return p, fmt.Errorf("图片解码校验失败：%w", e)
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
		return fmt.Errorf("目录不在当前下载目录中")
	}
	return nil
}
