package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"hitomi-downloader/internal/hitomi"
)

type ScanStatus struct {
	Root         string   `json:"root"`
	State        string   `json:"state"`
	Visited      int      `json:"visited"`
	Found        int      `json:"found"`
	Current      string   `json:"current"`
	Warnings     []string `json:"warnings"`
	WarningCount int      `json:"warningCount"`
	Error        string   `json:"error,omitempty"`
}
type LibraryPage struct {
	Tags        []TagFacet `json:"tags"`
	Entries     []Entry    `json:"entries"`
	Total       int        `json:"total"`
	LegacyTotal int        `json:"legacyTotal"`
	Page        int        `json:"page"`
	PageSize    int        `json:"pageSize"`
	Scan        ScanStatus `json:"scan"`
}

type MigrationTargets struct {
	Directories []string `json:"directories"`
	Skipped     int      `json:"skipped"`
}

// 移行の進行で一覧が縮小しても取りこぼさないよう、対象を一度に確定する。
func (m *Manager) MigrationTargets() (MigrationTargets, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := MigrationTargets{Directories: []string{}}
	if m.closing {
		return result, fmt.Errorf("服务正在停止")
	}
	m.library.mu.Lock()
	defer m.library.mu.Unlock()
	if m.library.status.Root != m.root || m.library.status.State != "completed" {
		return result, fmt.Errorf("请等待目录扫描完成后再全部迁移")
	}
	for _, entry := range m.library.entries {
		if !entry.Legacy {
			continue
		}
		if _, active := m.cancel[entry.ID]; active {
			result.Skipped++
			continue
		}
		result.Directories = append(result.Directories, entry.Directory)
	}
	sort.Strings(result.Directories)
	return result, nil
}

type libraryIndex struct {
	mu         sync.Mutex
	status     ScanStatus
	entries    []Entry
	byDir      map[string]int
	cancel     context.CancelFunc
	generation uint64
}

func entryFrom(dir string, meta Metadata, g hitomi.Gallery) Entry {
	e := Entry{ID: g.Number(), Title: g.Title, Directory: dir, Pages: len(g.Files), UpdatedAt: meta.UpdatedAt, Legacy: meta.Legacy, Language: g.Language, Type: g.Type, JapaneseTitle: g.JapaneseTitle, Artists: []string{}, Formats: []string{}}
	e.Tags = parseTags(meta.Gallery)
	var extra struct {
		JapaneseTitle string          `json:"japaneseTitle"`
		Artists       json.RawMessage `json:"artists"`
	}
	_ = json.Unmarshal(meta.Gallery, &extra)
	if e.JapaneseTitle == "" {
		e.JapaneseTitle = extra.JapaneseTitle
	}
	if json.Unmarshal(extra.Artists, &e.Artists) != nil {
		e.Artists = []string{}
		var artists []struct {
			Artist string `json:"artist"`
		}
		if json.Unmarshal(extra.Artists, &artists) == nil {
			for _, a := range artists {
				e.Artists = append(e.Artists, a.Artist)
			}
		}
	}
	if e.Artists == nil {
		e.Artists = []string{}
	}
	seen := map[string]bool{}
	indices := map[int]bool{}
	for _, p := range meta.Pages {
		if !seen[p.Format] {
			e.Formats = append(e.Formats, p.Format)
			seen[p.Format] = true
		}
		if p.Fallback != "" {
			e.Fallbacks++
		}
		if p.Index >= 0 && p.Index < len(g.Files) {
			indices[p.Index] = true
		}
	}
	sort.Strings(e.Formats)
	if u := meta.Upstream; u != nil && (u.Status == "changed" || u.Status == "deleted") {
		e.Upstream, e.UpstreamNote = u.Status, u.Summary()
	}
	if !e.Legacy {
		e.Missing = max(0, e.Pages-len(indices))
	}
	return e
}

// 作品ディレクトリでは metadata を先に探し、画像ファイルの全列挙を避ける。
func walkLibrary(ctx context.Context, root string, emit func(Entry), progress func(string, string)) error {
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		progress(dir, "")
		if depth > 64 {
			progress(dir, "目录层级超过限制："+dir)
			return nil
		}
		info, e := os.Lstat(filepath.Join(dir, "metadata.json"))
		if e == nil {
			if !info.Mode().IsRegular() {
				progress(dir, "metadata 不是普通文件："+dir)
				return nil
			}
			meta, g, e := readMetadata(dir)
			if err := ctx.Err(); err != nil {
				return err
			}
			if e != nil {
				progress(dir, dir+": "+e.Error())
				return nil
			}
			entry := entryFrom(dir, meta, g)
			if entry.UpdatedAt.IsZero() {
				entry.UpdatedAt = info.ModTime()
			}
			emit(entry)
			return nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			progress(dir, dir+": "+e.Error())
			return nil
		}
		f, e := os.Open(dir)
		if e != nil {
			progress(dir, dir+": "+e.Error())
			return nil
		}
		defer f.Close()
		for {
			if e := ctx.Err(); e != nil {
				return e
			}
			children, e := f.ReadDir(128)
			if e != nil && e != io.EOF {
				progress(dir, dir+": "+e.Error())
				return nil
			}
			for _, child := range children {
				if child.IsDir() && !strings.HasPrefix(child.Name(), ".") {
					if e := walk(filepath.Join(dir, child.Name()), depth+1); e != nil {
						return e
					}
				}
			}
			if e == io.EOF {
				return nil
			}
		}
	}
	return walk(root, 0)
}
func (m *Manager) StartScan(force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return fmt.Errorf("服务正在停止")
	}
	root := m.root
	m.library.mu.Lock()
	defer m.library.mu.Unlock()
	s := m.library.status
	if s.Root == root && (s.State == "running" || (!force && s.State != "")) {
		return nil
	}
	if m.library.cancel != nil {
		m.library.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.library.cancel = cancel
	m.library.generation++
	gen := m.library.generation
	m.library.entries = []Entry{}
	m.library.byDir = map[string]int{}
	m.library.status = ScanStatus{Root: root, State: "running", Warnings: []string{}}
	// 読み取り専用の走査は、切断されたマウントの OS I/O 待ちで終了を妨げない。
	go func() {
		e := walkLibrary(ctx, root, func(e Entry) {
			m.library.mu.Lock()
			defer m.library.mu.Unlock()
			if gen != m.library.generation {
				return
			}
			if i, ok := m.library.byDir[e.Directory]; ok {
				old := m.library.entries[i]
				if (!e.Legacy || old.Legacy) && (old.Legacy && !e.Legacy || e.UpdatedAt.After(old.UpdatedAt)) {
					m.library.entries[i] = e
				}
				return
			}
			m.library.byDir[e.Directory] = len(m.library.entries)
			m.library.entries = append(m.library.entries, e)
			m.library.status.Found++
		}, func(path, warning string) {
			m.library.mu.Lock()
			defer m.library.mu.Unlock()
			if gen != m.library.generation {
				return
			}
			m.library.status.Current = path
			if warning == "" {
				m.library.status.Visited++
			} else {
				m.library.status.WarningCount++
				if len(m.library.status.Warnings) < 100 {
					m.library.status.Warnings = append(m.library.status.Warnings, warning)
				}
			}
		})
		m.library.mu.Lock()
		defer m.library.mu.Unlock()
		if gen != m.library.generation {
			return
		}
		m.library.status.State = "completed"
		if errors.Is(e, context.Canceled) || ctx.Err() != nil {
			m.library.status.State = "cancelled"
		} else if e != nil {
			m.library.status.State = "failed"
			m.library.status.Error = e.Error()
		}
	}()
	return nil
}
func (m *Manager) CancelScan() {
	m.library.mu.Lock()
	defer m.library.mu.Unlock()
	if m.library.cancel != nil {
		m.library.cancel()
	}
}
func (m *Manager) Library(page, size int, q, kind string, tags ...string) (LibraryPage, error) {
	if e := m.StartScan(false); e != nil {
		return LibraryPage{}, e
	}
	m.library.mu.Lock()
	defer m.library.mu.Unlock()
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	s := m.library.status
	s.Warnings = append([]string{}, s.Warnings...)
	out := LibraryPage{Entries: []Entry{}, Page: page, PageSize: size, Scan: s}
	q = strings.ToLower(strings.TrimSpace(q))
	filtered := []Entry{}
	counts := map[string]int{}
	for _, entry := range m.library.entries {
		if entry.Legacy {
			out.LegacyTotal++
		}
		if kind == "legacy" && !entry.Legacy || kind == "native" && entry.Legacy || kind == "upstream" && entry.Upstream == "" {
			continue
		}
		if !matchesQuery(entry, q) {
			continue
		}
		for _, tag := range entry.Tags {
			counts[tagValue(tag)]++
		}
		match := true
		for _, tag := range tags {
			if strings.TrimSpace(tag) != "" && !hasTag(entry, tag) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		filtered = append(filtered, entry)
	}
	out.Tags = tagFacets(counts)
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt) })
	out.Total = len(filtered)
	start := (page - 1) * size
	if start >= 0 && start < len(filtered) {
		out.Entries = append(out.Entries, filtered[start:min(start+size, len(filtered))]...)
	}
	return out, nil
}
func (m *Manager) refreshIndex(dir string) {
	meta, g, e := readMetadata(dir)
	if e != nil {
		return
	}
	entry := entryFrom(dir, meta, g)
	m.library.mu.Lock()
	defer m.library.mu.Unlock()
	root := m.library.status.Root
	if root == "" {
		return
	}
	rel, e := filepath.Rel(root, dir)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return
	}
	if i, ok := m.library.byDir[dir]; ok {
		m.library.entries[i] = entry
		return
	}
	if m.library.byDir == nil {
		m.library.byDir = map[string]int{}
	}
	m.library.byDir[dir] = len(m.library.entries)
	m.library.entries = append(m.library.entries, entry)
	m.library.status.Found++
}

func (m *Manager) LocalCover(dir string) (string, error) {
	if e := within(m.Root(), dir); e != nil {
		return "", e
	}
	meta, _, e := readMetadata(dir)
	if e != nil {
		return "", e
	}
	names := []string{}
	for _, p := range meta.Pages {
		if p.Index == 0 && filepath.Base(p.Name) == p.Name {
			names = append(names, p.Name)
		}
	}
	if meta.Legacy {
		for _, ext := range []string{"webp", "avif", "png", "jpg", "jpeg", "gif"} {
			names = append(names, "0001."+ext)
		}
	}
	for _, name := range names {
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".jxl" {
			continue
		}
		path := filepath.Join(dir, name)
		info, e := os.Lstat(path)
		if e == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= 256<<20 {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}
