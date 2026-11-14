package hitomi

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fake(t *testing.T, f func(*http.Request) (int, []byte)) *Client {
	t.Helper()
	c, _ := New("direct")
	c.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		status, b := f(r)
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(b))), Header: http.Header{}, Request: r}, nil
	})
	return c
}
func ids(values ...int) []byte {
	b := make([]byte, 4*len(values))
	for i, n := range values {
		binary.BigEndian.PutUint32(b[i*4:], uint32(n))
	}
	return b
}
func TestSearchDoesNotResetEmptyIntersection(t *testing.T) {
	c := fake(t, func(r *http.Request) (int, []byte) {
		switch r.URL.Path {
		case "/n/artist/empty-all.nozomi":
			return 200, ids()
		case "/n/artist/full-all.nozomi":
			return 200, ids(3, 2, 1)
		}
		t.Fatal(r.URL)
		return 404, nil
	})
	got, e := c.Search(context.Background(), "artist:empty artist:full")
	if e != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, e)
	}
}
func TestSearchErrorIsNotEmptySuccess(t *testing.T) {
	c := fake(t, func(r *http.Request) (int, []byte) { return 403, nil })
	_, e := c.Search(context.Background(), "artist:test")
	var he *Error
	if !errors.As(e, &he) || he.Status != 403 {
		t.Fatalf("%v", e)
	}
}
func TestNegativeOnlySearch(t *testing.T) {
	c := fake(t, func(r *http.Request) (int, []byte) {
		if strings.Contains(r.URL.Path, "/artist/") {
			return 200, ids(2)
		}
		return 200, ids(3, 2, 1)
	})
	got, e := c.Search(context.Background(), "-artist:test")
	if e != nil || len(got) != 2 || got[0] != 3 || got[1] != 1 {
		t.Fatalf("%v %v", got, e)
	}
}
func TestMalformedBinaryIsRejected(t *testing.T) {
	if _, e := decodeIDs([]byte{1}); e == nil {
		t.Fatal("accepted truncated nozomi")
	}
	for _, b := range [][]byte{nil, ids(99), ids(1, 99)} {
		if _, e := decodeNode(b); e == nil {
			t.Fatal("accepted malformed node")
		}
	}
}
func TestOriginalFormatFirst(t *testing.T) {
	got := Formats(File{Name: "source.png", HasWebp: 1, HasAvif: 1, HasJxl: 1})
	if strings.Join(got, ",") != "png,jxl,avif,webp" {
		t.Fatal(got)
	}
}
func TestRoutingRejectsInvalidRefreshWithoutPoisoning(t *testing.T) {
	broken := false
	c := fake(t, func(r *http.Request) (int, []byte) {
		if broken {
			return 200, []byte("html error")
		}
		return 200, []byte("var o = 0; switch(g) {case 1: o = 1; break;} b: '123/'")
	})
	f := File{Hash: strings.Repeat("0", 63) + "1", Name: "image.png"}
	first, e := c.URL(context.Background(), f, "png", false)
	// 末尾の桁順序とデフォルトのサブドメインを検証する。
	if e != nil || !strings.Contains(first, "1.gold-usergeneratedcontent.net/123/256/") {
		t.Fatalf("%s %v", first, e)
	}
	broken = true
	if _, e = c.URL(context.Background(), f, "png", true); e == nil {
		t.Fatal("accepted invalid gg.js")
	}
	got, e := c.URL(context.Background(), f, "png", false)
	if e != nil || got != first {
		t.Fatalf("cache poisoned: %s %v", got, e)
	}
}
func TestRangeRequires206(t *testing.T) {
	c := fake(t, func(r *http.Request) (int, []byte) { return 200, ids(1) })
	_, e := c.Bytes(context.Background(), "https://example.test/index", "bytes=0-3", 4)
	if e == nil {
		t.Fatal("accepted full response for range")
	}
}
func TestCancelledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := fake(t, func(r *http.Request) (int, []byte) { t.Fatal("request after cancellation"); return 200, nil })
	_, e := c.Bytes(ctx, "https://example.test", "", 10)
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("%v", e)
	}
}

func TestLongRetryAfterReturnsRecoverableError(t *testing.T) {
	c, _ := New("direct")
	calls := 0
	c.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	_, e := c.Bytes(context.Background(), "https://example.test", "", 10)
	var he *Error
	if !errors.As(e, &he) || he.Code != "RATE_LIMITED" || !he.Retryable || calls != 1 {
		t.Fatalf("%v calls=%d", e, calls)
	}
}

func TestKeywordSearchReadsVersionedBTreeAndData(t *testing.T) {
	sum := sha256.Sum256([]byte("hello"))
	node := make([]byte, 464)
	binary.BigEndian.PutUint32(node, 1)
	binary.BigEndian.PutUint32(node[4:], 4)
	copy(node[8:12], sum[:4])
	binary.BigEndian.PutUint32(node[12:], 1)
	binary.BigEndian.PutUint32(node[24:], 12)
	c := fake(t, func(r *http.Request) (int, []byte) {
		switch r.URL.Path {
		case "/galleriesindex/version":
			return 200, []byte("123")
		case "/galleriesindex/galleries.123.index":
			if r.Header.Get("Range") != "bytes=0-463" {
				t.Error(r.Header)
			}
			return 206, node
		case "/galleriesindex/galleries.123.data":
			if r.Header.Get("Range") != "bytes=0-11" {
				t.Error(r.Header)
			}
			return 206, ids(2, 42, 7)
		}
		t.Fatal(r.URL)
		return 404, nil
	})
	got, e := c.Search(context.Background(), "hello")
	if e != nil || len(got) != 2 || got[0] != 42 || got[1] != 7 {
		t.Fatalf("%v %v", got, e)
	}
}
