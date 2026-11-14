package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hitomi-downloader/internal/hitomi"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(b)
}

func (m *Manager) Migrate(dir string) error {
	if e := within(m.Root(), dir); e != nil {
		return e
	}
	meta, g, e := readMetadata(dir)
	if e != nil {
		return e
	}
	if !meta.Legacy {
		return fmt.Errorf("该作品已使用新 metadata，无需迁移")
	}
	return m.Start(g.Number(), dir, "migrate")
}

func normalizeLegacy(raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return nil, e
	}
	for old, current := range map[string]string{"japaneseTitle": "japanese_title", "languageLocalname": "language_localname", "sceneIndexes": "scene_indexes", "coverUrl": "cover_url"} {
		if _, ok := fields[current]; !ok && fields[old] != nil {
			fields[current] = fields[old]
		}
	}
	for field, key := range map[string]string{"artists": "artist", "groups": "group", "parodys": "parody", "characters": "character"} {
		var names []string
		if json.Unmarshal(fields[field], &names) == nil && names != nil {
			objects := []map[string]string{}
			for _, name := range names {
				objects = append(objects, map[string]string{key: name})
			}
			fields[field], _ = json.Marshal(objects)
		}
	}
	return json.Marshal(fields)
}

func inspectPage(ctx context.Context, dir, name string, index int, source hitomi.File) (Page, error) {
	var p Page
	if filepath.Base(name) != name {
		return p, fmt.Errorf("无效图片路径")
	}
	path := filepath.Join(dir, name)
	info, e := os.Lstat(path)
	if e != nil {
		return p, e
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 256<<20 {
		return p, fmt.Errorf("图片不是有效普通文件或超过大小限制")
	}
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	head := make([]byte, 512)
	n, e := io.ReadFull(contextReader{ctx, f}, head)
	if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
		return p, e
	}
	head = head[:n]
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if !imageSignature(head, ext) {
		return p, fmt.Errorf("图片签名与格式不符")
	}
	if ext == "webp" && int64(binary.LittleEndian.Uint32(head[4:8]))+8 != info.Size() {
		return p, fmt.Errorf("WebP 内容长度不完整")
	}
	if ext == "png" || ext == "jpg" || ext == "jpeg" || ext == "gif" {
		if _, e = f.Seek(0, 0); e != nil {
			return p, e
		}
		cfg, _, e := image.DecodeConfig(contextReader{ctx, f})
		if e != nil {
			return p, e
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 100_000_000 {
			return p, fmt.Errorf("图片尺寸无效或超过限制")
		}
	}
	if _, e = f.Seek(0, 0); e != nil {
		return p, e
	}
	hash := sha256.New()
	reader := contextReader{ctx, io.TeeReader(f, hash)}
	if ext == "png" || ext == "jpg" || ext == "jpeg" || ext == "gif" {
		if _, _, e = image.Decode(reader); e != nil {
			return p, e
		}
	}
	if _, e = io.Copy(io.Discard, reader); e != nil {
		return p, e
	}
	after, e := f.Stat()
	if e != nil {
		return p, e
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return p, fmt.Errorf("迁移期间图片发生变化")
	}
	return Page{Index: index, Name: name, SourceHash: source.Hash, Format: ext, Bytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func backupLegacy(dir string, original []byte) error {
	path := filepath.Join(dir, "metadata.legacy.json")
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(e, os.ErrExist) {
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if !bytes.Equal(b, original) {
			return fmt.Errorf("已有 metadata.legacy.json 与当前旧 metadata 不同，停止迁移以保留备份")
		}
		return nil
	}
	if e != nil {
		return e
	}
	if _, e = f.Write(original); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}

func (m *Manager) migrate(ctx context.Context, task Task) error {
	dir := task.Directory
	if e := within(task.Root, dir); e != nil {
		return e
	}
	meta, g, e := readMetadata(dir)
	if e != nil {
		return e
	}
	if g.Number() != task.ID {
		return fmt.Errorf("metadata ID 与迁移任务不一致")
	}
	if !meta.Legacy {
		if strings.HasPrefix(meta.QualityPolicy, "migrated-existing-files") {
			m.refreshIndex(dir)
			return m.update(task.ID, func(t *Task) {
				t.Title = g.Title
				t.Total = len(g.Files)
				t.Done = len(g.Files)
				seen := map[int]bool{}
				for _, p := range meta.Pages {
					seen[p.Index] = true
				}
				for i := range g.Files {
					if !seen[i] {
						t.Errors[i+1] = "该页尚未建立校验记录"
					}
				}
				if len(t.Errors) > 0 {
					t.Warning = "迁移已完成，部分页面仍需验证并补全"
				}
			})
		}
		return fmt.Errorf("metadata 已迁移，请重新扫描")
	}
	original, e := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if e != nil {
		return e
	}
	if !bytes.Equal(original, meta.Gallery) {
		return fmt.Errorf("读取期间 metadata 发生变化，请重试")
	}
	raw, e := normalizeLegacy(meta.Gallery)
	if e != nil {
		return e
	}
	if e = m.update(task.ID, func(t *Task) { t.Title = g.Title; t.Total = len(g.Files); t.Done = 0 }); e != nil {
		return e
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	candidates := map[string][]string{}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".jxl":
			base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			candidates[base] = append(candidates[base], entry.Name())
		}
	}
	pages := []Page{}
	issues := 0
	for i, file := range g.Files {
		if e := ctx.Err(); e != nil {
			return e
		}
		names := candidates[fmt.Sprintf("%04d", i+1)]
		var page Page
		var problem error
		if len(names) == 0 {
			problem = fmt.Errorf("缺少图片")
		} else if len(names) > 1 {
			problem = fmt.Errorf("同一页存在多个格式，请整理后再补全：%s", strings.Join(names, ", "))
		} else {
			page, problem = inspectPage(ctx, dir, names[0], i, file)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if problem == nil {
			pages = append(pages, page)
		} else {
			issues++
		}
		if e = m.update(task.ID, func(t *Task) {
			t.Done++
			if problem != nil {
				t.Errors[i+1] = problem.Error()
			}
		}); e != nil {
			return e
		}
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	current, e := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if e != nil {
		return e
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("迁移期间 metadata 发生变化，已停止写入")
	}
	if e = backupLegacy(dir, original); e != nil {
		return e
	}
	meta = Metadata{Schema: 1, Gallery: raw, Pages: pages, UpdatedAt: time.Now(), QualityPolicy: "migrated-existing-files; local integrity baseline, not original-quality verification"}
	if e = AtomicJSON(filepath.Join(dir, "metadata.json"), meta); e != nil {
		return e
	}
	m.refreshIndex(dir)
	return m.update(task.ID, func(t *Task) {
		if issues > 0 {
			t.Warning = fmt.Sprintf("迁移完成，%d 页缺失、损坏或存在歧义；可查看详情后验证并补全", issues)
		}
	})
}
