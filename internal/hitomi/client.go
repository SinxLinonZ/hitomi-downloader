package hitomi

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Status    int    `json:"upstreamStatus,omitempty"`
}

func (e *Error) Error() string { return e.Message }

type File struct {
	Name    string `json:"name"`
	Hash    string `json:"hash"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	HasWebp int    `json:"haswebp"`
	HasAvif int    `json:"hasavif"`
	HasJxl  int    `json:"hasjxl"`
}
type Gallery struct {
	ID            json.Number     `json:"id"`
	Title         string          `json:"title"`
	JapaneseTitle string          `json:"japanese_title"`
	Type          string          `json:"type"`
	Language      string          `json:"language"`
	Files         []File          `json:"files"`
	Raw           json.RawMessage `json:"-"`
}

func (g Gallery) Number() int { n, _ := strconv.Atoi(string(g.ID)); return n }

type routing struct {
	B       string
	Default int
	Cases   map[int]bool
	Other   int
	At      time.Time
}
type Client struct {
	HTTP      *http.Client
	Base      string
	mu        sync.Mutex
	next      time.Time
	routeMu   sync.Mutex
	route     routing
	versionMu sync.Mutex
	// Backoff は再試行間隔の基準値。0 のときは 1 秒（テストでは短くする）。
	Backoff   time.Duration
	version   string
	versionAt time.Time
}

func New(proxy string) (*Client, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxConnsPerHost = 6
	t.ResponseHeaderTimeout = 20 * time.Second
	if proxy == "direct" {
		t.Proxy = nil
	} else if proxy != "" {
		u, e := url.Parse(proxy)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
			return nil, fmt.Errorf("无效 Proxy URL")
		}
		t.Proxy = http.ProxyURL(u)
	}
	return &Client{HTTP: &http.Client{Transport: t, Timeout: 90 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}, Base: "https://ltn.gold-usergeneratedcontent.net"}, nil
}
func wait(ctx context.Context, d time.Duration) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type attemptsKey struct{}

// WithMaxAttempts は、この context で発行する要求の最大試行回数を指定する（既定は 4）。
func WithMaxAttempts(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, attemptsKey{}, n)
}
func (c *Client) request(ctx context.Context, u, ran string) (*http.Response, error) {
	attempts := 4
	if n, ok := ctx.Value(attemptsKey{}).(int); ok && n > 0 {
		attempts = n
	}
	for attempt := 0; attempt < attempts; attempt++ {
		c.mu.Lock()
		now := time.Now()
		at := c.next
		if at.Before(now) {
			at = now
		}
		c.next = at.Add(200 * time.Millisecond)
		c.mu.Unlock()
		if e := wait(ctx, time.Until(at)); e != nil {
			return nil, e
		}
		req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Referer", "https://hitomi.la/")
		req.Header.Set("User-Agent", "HitomiLocal/0.1")
		if ran != "" {
			req.Header.Set("Range", ran)
		}
		resp, e := c.HTTP.Do(req)
		if e == nil && (resp.StatusCode == 200 || resp.StatusCode == 206) {
			return resp, nil
		}
		retry := true
		status := 0
		base := c.Backoff
		if base <= 0 {
			base = time.Second
		}
		delay := time.Duration(1<<attempt)*base + rand.N(base/2+1)
		if resp != nil {
			status = resp.StatusCode
			retry = status == 408 || status == 429 || status >= 500
			if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 {
				delay = time.Duration(n) * time.Second
			}
			if at, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil && time.Until(at) > 0 {
				delay = time.Until(at)
			}
			resp.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		code := "UPSTREAM_HTTP"
		if status == 0 {
			code = "UPSTREAM_NETWORK"
		}
		if status == 404 {
			code = "NOT_FOUND"
		}
		// ドメイン自体が存在しない場合は再試行しても無駄なので、呼び出し側が別形式へ回退できるよう即座に返す。
		var dns *net.DNSError
		if e != nil && errors.As(e, &dns) && dns.IsNotFound {
			code, retry = "UPSTREAM_DNS", false
		}
		failure := &Error{code, fmt.Sprintf("上游请求失败（HTTP %d，尝试 %d/%d）", status, attempt+1, attempts), retry, status}
		if e != nil {
			failure.Message = "网络请求失败：" + e.Error()
		}
		if !retry || attempt == attempts-1 {
			return nil, failure
		}
		if delay > 60*time.Second {
			return nil, &Error{"RATE_LIMITED", "上游要求较长等待，请稍后重试", true, status}
		}
		if e := wait(ctx, delay); e != nil {
			return nil, e
		}
	}
	panic("unreachable")
}
func (c *Client) Bytes(ctx context.Context, u, ran string, limit int64) ([]byte, error) {
	r, e := c.request(ctx, u, ran)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if ran != "" && r.StatusCode != 206 {
		return nil, &Error{"INVALID_RANGE", "上游未返回 Partial Content", true, r.StatusCode}
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if e == nil && int64(len(b)) > limit {
		e = fmt.Errorf("上游响应超过大小限制")
	}
	return b, e
}
func (c *Client) Image(ctx context.Context, u string) (*http.Response, error) {
	return c.request(ctx, u, "")
}
func (c *Client) Gallery(ctx context.Context, id int) (Gallery, error) {
	var g Gallery
	b, e := c.Bytes(ctx, fmt.Sprintf("%s/galleries/%d.js", c.Base, id), "", 16<<20)
	if e != nil {
		return g, e
	}
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "var galleryinfo = ") {
		return g, &Error{"PROTOCOL_CHANGED", "无法识别 gallery metadata 格式", false, 0}
	}
	s = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(s, "var galleryinfo = ")), ";")
	if e = json.Unmarshal([]byte(s), &g); e != nil {
		return g, &Error{"PROTOCOL_CHANGED", "无法解析 gallery metadata：" + e.Error(), false, 0}
	}
	if g.Number() != id || len(g.Files) == 0 {
		return g, fmt.Errorf("gallery ID 或页面列表无效")
	}
	g.Raw = json.RawMessage(s)
	return g, nil
}
func (c *Client) Version(ctx context.Context, force bool) (string, error) {
	// レート制御の c.mu とは別のロックでキャッシュを保護し、取得中も c.mu を保持しない。
	c.versionMu.Lock()
	defer c.versionMu.Unlock()
	if !force && time.Since(c.versionAt) < 5*time.Minute && c.version != "" {
		return c.version, nil
	}
	b, e := c.Bytes(ctx, c.Base+"/galleriesindex/version", "", 128)
	if e != nil {
		return "", e
	}
	v := strings.TrimSpace(string(b))
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(v) {
		return "", fmt.Errorf("无效 index version")
	}
	c.version = v
	c.versionAt = time.Now()
	return v, nil
}
func decodeIDs(b []byte) ([]int, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("nozomi 数据被截断")
	}
	ids := make([]int, 0, len(b)/4)
	for i := 0; i < len(b); i += 4 {
		n := int(binary.BigEndian.Uint32(b[i:]))
		if n <= 0 {
			return nil, fmt.Errorf("无效 gallery ID")
		}
		ids = append(ids, n)
	}
	return ids, nil
}

type node struct {
	keys  [][]byte
	data  [][2]uint64
	child []uint64
}

func decodeNode(b []byte) (node, error) {
	var n node
	pos := 0
	read := func(size int) ([]byte, error) {
		if size < 0 || pos+size > len(b) {
			return nil, fmt.Errorf("index 数据被截断")
		}
		v := b[pos : pos+size]
		pos += size
		return v, nil
	}
	num := func() (uint32, error) {
		v, e := read(4)
		if e != nil {
			return 0, e
		}
		return binary.BigEndian.Uint32(v), nil
	}
	count, e := num()
	if e != nil || count > 16 {
		return n, fmt.Errorf("无效 index key count")
	}
	for i := uint32(0); i < count; i++ {
		size, e := num()
		if e != nil || size == 0 || size > 32 {
			return n, fmt.Errorf("无效 index key size")
		}
		v, e := read(int(size))
		if e != nil {
			return n, e
		}
		n.keys = append(n.keys, v)
	}
	count, e = num()
	if e != nil || int(count) != len(n.keys) {
		return n, fmt.Errorf("无效 index data count")
	}
	for i := uint32(0); i < count; i++ {
		v, e := read(12)
		if e != nil {
			return n, e
		}
		n.data = append(n.data, [2]uint64{binary.BigEndian.Uint64(v), uint64(binary.BigEndian.Uint32(v[8:]))})
	}
	for i := 0; i < 17; i++ {
		v, e := read(8)
		if e != nil {
			return n, e
		}
		n.child = append(n.child, binary.BigEndian.Uint64(v))
	}
	return n, nil
}
func (c *Client) term(ctx context.Context, term, version string) ([]int, error) {
	if ns, tag, ok := strings.Cut(term, ":"); ok {
		if tag == "" {
			return nil, fmt.Errorf("标签值不能为空")
		}
		path := ""
		switch ns {
		case "language":
			path = "index-" + url.PathEscape(tag)
		case "female", "male":
			path = "tag/" + url.PathEscape(ns+":"+tag) + "-all"
		default:
			if !regexp.MustCompile(`^(artist|group|series|character|type|tag)$`).MatchString(ns) {
				return nil, fmt.Errorf("不支持的 namespace：%s", ns)
			}
			path = ns + "/" + url.PathEscape(tag) + "-all"
		}
		b, e := c.Bytes(ctx, c.Base+"/n/"+path+".nozomi", "", 64<<20)
		if e != nil {
			var he *Error
			if errors.As(e, &he) && he.Status == 404 {
				return []int{}, nil
			}
			return nil, e
		}
		return decodeIDs(b)
	}
	sum := sha256.Sum256([]byte(term))
	key := sum[:4]
	address := uint64(0)
	seen := map[uint64]bool{}
	for depth := 0; depth < 32; depth++ {
		if seen[address] {
			return nil, fmt.Errorf("index 节点循环")
		}
		seen[address] = true
		b, e := c.Bytes(ctx, c.Base+"/galleriesindex/galleries."+version+".index", fmt.Sprintf("bytes=%d-%d", address, address+463), 464)
		if e != nil {
			return nil, e
		}
		n, e := decodeNode(b)
		if e != nil {
			return nil, e
		}
		i := 0
		for i < len(n.keys) && strings.Compare(string(key), string(n.keys[i])) > 0 {
			i++
		}
		if i < len(n.keys) && string(key) == string(n.keys[i]) {
			d := n.data[i]
			if d[1] < 4 || d[1] > 64<<20 || d[0] > 1<<50 {
				return nil, fmt.Errorf("index range 超出限制")
			}
			data, e := c.Bytes(ctx, c.Base+"/galleriesindex/galleries."+version+".data", fmt.Sprintf("bytes=%d-%d", d[0], d[0]+d[1]-1), int64(d[1]))
			if e != nil {
				return nil, e
			}
			if len(data) < 4 || uint64(len(data)) != d[1] || int(binary.BigEndian.Uint32(data))*4 != len(data)-4 {
				return nil, fmt.Errorf("index ID 数据不完整")
			}
			return decodeIDs(data[4:])
		}
		address = n.child[i]
		if address == 0 {
			return []int{}, nil
		}
	}
	return nil, fmt.Errorf("index 深度超出限制")
}
func (c *Client) Search(ctx context.Context, q string) ([]int, error) {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(q)))
	if len(terms) > 20 {
		return nil, fmt.Errorf("搜索词不得超过 20 个")
	}
	positive := [][]int{}
	negative := [][]int{}
	version := ""
	for _, term := range terms {
		neg := strings.HasPrefix(term, "-")
		term = strings.TrimPrefix(term, "-")
		term = strings.ReplaceAll(term, "_", " ")
		if term == "" {
			return nil, fmt.Errorf("搜索词不能为空")
		}
		if !strings.Contains(term, ":") && version == "" {
			v, e := c.Version(ctx, false)
			if e != nil {
				return nil, e
			}
			version = v
		}
		ids, e := c.term(ctx, term, version)
		if e != nil {
			return nil, e
		}
		if neg {
			negative = append(negative, ids)
		} else {
			positive = append(positive, ids)
		}
	}
	var result []int
	if len(positive) == 0 {
		b, e := c.Bytes(ctx, c.Base+"/n/index-all.nozomi", "", 64<<20)
		if e != nil {
			return nil, e
		}
		result, e = decodeIDs(b)
		if e != nil {
			return nil, e
		}
	} else {
		result = positive[0]
		positive = positive[1:]
	}
	for _, ids := range positive {
		set := map[int]bool{}
		for _, id := range ids {
			set[id] = true
		}
		out := make([]int, 0)
		for _, id := range result {
			if set[id] {
				out = append(out, id)
			}
		}
		result = out
	}
	for _, ids := range negative {
		set := map[int]bool{}
		for _, id := range ids {
			set[id] = true
		}
		out := make([]int, 0)
		for _, id := range result {
			if !set[id] {
				out = append(out, id)
			}
		}
		result = out
	}
	return result, nil
}
func (c *Client) routing(ctx context.Context, force bool) (routing, error) {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	if !force && c.route.B != "" && time.Since(c.route.At) < time.Minute {
		return c.route, nil
	}
	b, e := c.Bytes(ctx, c.Base+"/gg.js", "", 1<<20)
	if e != nil {
		return routing{}, e
	}
	s := string(b)
	def := regexp.MustCompile(`var o\s*=\s*([01])`).FindStringSubmatch(s)
	other := regexp.MustCompile(`o\s*=\s*([01]);\s*break;`).FindStringSubmatch(s)
	path := regexp.MustCompile(`b:\s*'([^']+)'`).FindStringSubmatch(s)
	cases := regexp.MustCompile(`case\s+(\d+):`).FindAllStringSubmatch(s, -1)
	if len(def) != 2 || len(other) != 2 || len(path) != 2 || len(cases) == 0 || !regexp.MustCompile(`^[0-9]+/?$`).MatchString(path[1]) {
		return routing{}, &Error{"PROTOCOL_CHANGED", "gg.js 路由规则已变化，需要更新适配器", false, 0}
	}
	r := routing{B: strings.TrimRight(path[1], "/") + "/", Cases: map[int]bool{}, At: time.Now()}
	r.Default, _ = strconv.Atoi(def[1])
	r.Other, _ = strconv.Atoi(other[1])
	for _, v := range cases {
		n, _ := strconv.Atoi(v[1])
		r.Cases[n] = true
	}
	c.route = r
	return r, nil
}
func (c *Client) URL(ctx context.Context, f File, format string, force bool) (string, error) {
	if len(f.Hash) != 64 {
		return "", fmt.Errorf("无效图片 hash")
	}
	if _, e := hex.DecodeString(f.Hash); e != nil {
		return "", e
	}
	r, e := c.routing(ctx, force)
	if e != nil {
		return "", e
	}
	n, _ := strconv.ParseInt(f.Hash[63:]+f.Hash[61:63], 16, 32)
	m := r.Default
	if r.Cases[int(n)] {
		m = r.Other
	}
	if format == "thumbnail" {
		return fmt.Sprintf("https://%ctn.gold-usergeneratedcontent.net/webpbigtn/%s/%s/%s.webp", 'a'+m, f.Hash[63:], f.Hash[61:63], f.Hash), nil
	}
	ext := strings.ToLower(strings.TrimPrefix(format, "."))
	prefix := ""
	if ext == "webp" || ext == "avif" || ext == "jxl" {
		prefix = ext[:1]
	}
	return fmt.Sprintf("https://%s%d.gold-usergeneratedcontent.net/%s%d/%s.%s", prefix, 1+m, r.B, n, f.Hash, ext), nil
}
func Formats(f File) []string {
	ext := ""
	if i := strings.LastIndex(f.Name, "."); i >= 0 {
		ext = strings.ToLower(f.Name[i+1:])
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		if regexp.MustCompile(`^(jpg|jpeg|png|gif|webp|avif|jxl)$`).MatchString(s) && !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	add(ext)
	if f.HasJxl != 0 {
		add("jxl")
	}
	if f.HasAvif != 0 {
		add("avif")
	}
	if f.HasWebp != 0 {
		add("webp")
	}
	return out
}
