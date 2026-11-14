package download

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hitomi-downloader/internal/hitomi"
)

// Upstream は Hitomi 側の gallery 情報と突き合わせた結果で、metadata.json に保存する。
// ページ番号は 1 始まり。
type Upstream struct {
	Status    string    `json:"status"` // synced | changed | deleted
	CheckedAt time.Time `json:"checkedAt"`
	Changed   []int     `json:"changed,omitempty"` // 上流でファイルが差し替えられたページ
	Added     []int     `json:"added,omitempty"`   // 上流にだけ存在するページ
	Removed   []int     `json:"removed,omitempty"` // 上流から消えたページ
	Message   string    `json:"message,omitempty"`
}

func (u *Upstream) Summary() string {
	if u == nil {
		return ""
	}
	if u.Status == "deleted" {
		return "上游已删除"
	}
	parts := []string{}
	if len(u.Changed) > 0 {
		parts = append(parts, fmt.Sprintf("替换 %d 页", len(u.Changed)))
	}
	if len(u.Added) > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 页", len(u.Added)))
	}
	if len(u.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("删除 %d 页", len(u.Removed)))
	}
	return strings.Join(parts, "，")
}

// diffFiles は、ローカルが記録している gallery と上流の gallery を hash で比較する。
// hash は元画像の識別子なので、ローカルが派生形式（webp など）でも同一ページなら一致する。
func diffFiles(local, remote []hitomi.File) (changed, added, removed []int) {
	for i := 0; i < len(local) || i < len(remote); i++ {
		switch {
		case i >= len(local):
			added = append(added, i+1)
		case i >= len(remote):
			removed = append(removed, i+1)
		case local[i].Hash != remote[i].Hash:
			changed = append(changed, i+1)
		}
	}
	return
}

// remoteState は Hitomi の情報と突き合わせた結果。
type remoteState struct {
	gallery  hitomi.Gallery
	ok       bool         // 最新の gallery 情報を取得できた
	deleted  bool         // 上流に作品が存在しない（404）
	upstream *Upstream    // 判明した上流の状態。取得できなかった場合は nil で、既存の記録を変えない
	note     string       // 取得できなかった理由
	changed  map[int]bool // 差し替えられたページ（0 始まり）
}

// checkUpstream は上流の gallery 情報を取得して差分を求める。
// 取得失敗は「破損」とは区別し、ローカル検証だけを続けられるようにする。
func (m *Manager) checkUpstream(ctx context.Context, id int, local hitomi.Gallery) (remoteState, error) {
	rs := remoteState{changed: map[int]bool{}}
	// 直近で Hitomi に接続できなかった場合、一括検証の各作品で待たされないよう、しばらく照会を省略する。
	m.mu.Lock()
	down := time.Since(m.upstreamDown) < time.Minute
	m.mu.Unlock()
	if down {
		rs.note = "最近一次连接 Hitomi 失败，已跳过联网对比，仅完成本地校验"
		return rs, nil
	}
	// 検証での照会は 2 回までに抑える（接続できない環境で長く待たせない）。
	g, e := m.client.Gallery(hitomi.WithMaxAttempts(ctx, 2), id)
	if ctx.Err() != nil {
		return rs, ctx.Err()
	}
	var he *hitomi.Error
	switch {
	case e == nil:
		rs.gallery, rs.ok = g, true
		changed, added, removed := diffFiles(local.Files, g.Files)
		for _, n := range changed {
			rs.changed[n-1] = true
		}
		u := &Upstream{Status: "synced", CheckedAt: time.Now()}
		if len(changed)+len(added)+len(removed) > 0 {
			u.Status, u.Changed, u.Added, u.Removed = "changed", changed, added, removed
		}
		rs.upstream = u
	case errors.As(e, &he) && he.Code == "NOT_FOUND":
		rs.deleted = true
		rs.upstream = &Upstream{Status: "deleted", CheckedAt: time.Now(), Message: "Hitomi 上已找不到该作品"}
		rs.note = "Hitomi 上已找不到该作品，本地内容保持不变"
	default:
		if he == nil || he.Code == "UPSTREAM_NETWORK" {
			m.mu.Lock()
			m.upstreamDown = time.Now()
			m.mu.Unlock()
		}
		rs.note = "未能获取 Hitomi 信息（" + e.Error() + "），仅完成本地校验，无法判断上游是否有更新"
	}
	return rs, nil
}

// supersede は上流で差し替え・削除された旧ページを superseded/<stamp>/ へ退避する。上書きや削除はしない。
func supersede(dir string, p Page, stamp string) error {
	if filepath.Base(p.Name) != p.Name {
		return fmt.Errorf("无效页面记录")
	}
	src := filepath.Join(dir, p.Name)
	if _, e := os.Lstat(src); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return e
	}
	dst := filepath.Join(dir, "superseded", stamp)
	if e := os.MkdirAll(dst, 0755); e != nil {
		return e
	}
	return os.Rename(src, filepath.Join(dst, p.Name))
}

// Update は、上流の変更（差し替え・新增・删除）をローカルに反映する。旧ページは superseded/ に残す。
func (m *Manager) Update(dir string) error {
	if e := within(m.Root(), dir); e != nil {
		return e
	}
	meta, g, e := readMetadata(dir)
	if e != nil {
		return e
	}
	if meta.Legacy {
		return fmt.Errorf("旧 metadata 需要先手动迁移，再应用上游更新")
	}
	return m.Start(g.Number(), dir, "update")
}
