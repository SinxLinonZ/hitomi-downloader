package download

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 閲覧用の API が返す作品情報。ファイルシステム上のパスは含めない。
type ReaderEntry struct {
	ID            int      `json:"id"`
	Title         string   `json:"title"`
	JapaneseTitle string   `json:"japaneseTitle,omitempty"`
	Artists       []string `json:"artists"`
	Language      string   `json:"language"`
	Type          string   `json:"type"`
	Pages         int      `json:"pages"`
	Tags          []Tag    `json:"tags"`
}
type ReaderLibraryPage struct {
	Entries  []ReaderEntry `json:"entries"`
	Tags     []TagFacet    `json:"tags"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
	Scanning bool          `json:"scanning"`
	Found    int           `json:"found"`
}
type ReaderPage struct {
	W int `json:"w"`
	H int `json:"h"`
}
type ReaderWork struct {
	ReaderEntry
	List []ReaderPage `json:"pageList"`
}

func readerEntry(e Entry) ReaderEntry {
	tags := e.Tags
	if tags == nil {
		tags = []Tag{}
	}
	return ReaderEntry{ID: e.ID, Title: e.Title, JapaneseTitle: e.JapaneseTitle, Artists: e.Artists, Language: e.Language, Type: e.Type, Pages: e.Pages, Tags: tags}
}

// ReaderLibrary は「已下载」と同じ検索・タグ絞り込みを、閲覧用の項目だけ返す形で提供する。
func (m *Manager) ReaderLibrary(page, size int, q string, tags ...string) (ReaderLibraryPage, error) {
	result, e := m.Library(page, size, q, "all", tags...)
	if e != nil {
		return ReaderLibraryPage{}, e
	}
	out := ReaderLibraryPage{Entries: make([]ReaderEntry, 0, len(result.Entries)), Tags: result.Tags, Total: result.Total, Page: result.Page, PageSize: result.PageSize, Scanning: result.Scan.State == "running", Found: result.Scan.Found}
	for _, entry := range result.Entries {
		out.Entries = append(out.Entries, readerEntry(entry))
	}
	return out, nil
}

// 同じ ID が複数のディレクトリにある場合は、新形式かつ更新が新しいものを選ぶ。
func (m *Manager) entryByID(id int) (Entry, bool) {
	m.library.mu.Lock()
	defer m.library.mu.Unlock()
	var best Entry
	found := false
	for _, entry := range m.library.entries {
		if entry.ID != id {
			continue
		}
		if !found || (best.Legacy && !entry.Legacy) || (best.Legacy == entry.Legacy && entry.UpdatedAt.After(best.UpdatedAt)) {
			best, found = entry, true
		}
	}
	return best, found
}

type readerWork struct {
	dir   string
	mtime time.Time
	names []string // ページ（0 始まり）ごとのファイル名。見つからないページは空文字
	pages []ReaderPage
	title ReaderEntry
}

var readerCache = struct {
	sync.Mutex
	items map[string]readerWork
}{items: map[string]readerWork{}}

// ErrScanning は、走査中でまだ作品が見つからないことを表す（待てば見つかる可能性がある）。
var ErrScanning = fmt.Errorf("目录扫描中，请稍后再试")

func (m *Manager) readerWork(id int) (readerWork, error) {
	if e := m.StartScan(false); e != nil {
		return readerWork{}, e
	}
	entry, ok := m.entryByID(id)
	if !ok {
		m.library.mu.Lock()
		scanning := m.library.status.State == "running"
		m.library.mu.Unlock()
		if scanning {
			return readerWork{}, ErrScanning
		}
		return readerWork{}, os.ErrNotExist
	}
	if e := within(m.Root(), entry.Directory); e != nil {
		return readerWork{}, e
	}
	info, e := os.Lstat(filepath.Join(entry.Directory, "metadata.json"))
	if e != nil {
		return readerWork{}, e
	}
	readerCache.Lock()
	cached, ok := readerCache.items[entry.Directory]
	readerCache.Unlock()
	if ok && cached.mtime.Equal(info.ModTime()) {
		return cached, nil
	}
	meta, g, e := readMetadata(entry.Directory)
	if e != nil {
		return readerWork{}, e
	}
	recorded := map[int]string{}
	for _, p := range meta.Pages {
		if filepath.Base(p.Name) == p.Name {
			recorded[p.Index] = p.Name
		}
	}
	w := readerWork{dir: entry.Directory, mtime: info.ModTime(), title: readerEntry(entry), names: make([]string, len(g.Files)), pages: make([]ReaderPage, len(g.Files))}
	for i, f := range g.Files {
		w.pages[i] = ReaderPage{W: f.Width, H: f.Height}
		if name := recorded[i]; name != "" {
			w.names[i] = name
			continue
		}
		// 旧形式（または記録のないページ）は 0001.ext の命名規則から探す。
		for _, ext := range []string{"webp", "avif", "jxl", "png", "jpg", "jpeg", "gif"} {
			name := fmt.Sprintf("%04d.%s", i+1, ext)
			if st, e := os.Lstat(filepath.Join(entry.Directory, name)); e == nil && st.Mode().IsRegular() {
				w.names[i] = name
				break
			}
		}
	}
	readerCache.Lock()
	if len(readerCache.items) >= 16 {
		readerCache.items = map[string]readerWork{}
	}
	readerCache.items[entry.Directory] = w
	readerCache.Unlock()
	return w, nil
}

// ReaderWork は閲覧画面に必要な作品情報とページ寸法を返す。
func (m *Manager) ReaderWork(id int) (ReaderWork, error) {
	w, e := m.readerWork(id)
	if e != nil {
		return ReaderWork{}, e
	}
	return ReaderWork{ReaderEntry: w.title, List: w.pages}, nil
}

// ReaderPageFile は n ページ目（1 始まり）の画像ファイルのパスを返す。
func (m *Manager) ReaderPageFile(id, n int) (string, error) {
	w, e := m.readerWork(id)
	if e != nil {
		return "", e
	}
	if n < 1 || n > len(w.names) || w.names[n-1] == "" {
		return "", os.ErrNotExist
	}
	path := filepath.Join(w.dir, w.names[n-1])
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 256<<20 {
		return "", os.ErrNotExist
	}
	return path, nil
}

// ReaderCoverFile は表紙として使う画像のパスを返す（最初のページ）。
func (m *Manager) ReaderCoverFile(id int) (string, error) {
	for n := 1; n <= 3; n++ {
		if path, e := m.ReaderPageFile(id, n); e == nil && !strings.HasSuffix(strings.ToLower(path), ".jxl") {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}
