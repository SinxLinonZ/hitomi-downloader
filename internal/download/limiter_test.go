package download

import (
	"context"
	"testing"
	"time"
)

func TestLimiterResizesWhileWaiting(t *testing.T) {
	l := newLimiter(1)
	if e := l.acquire(context.Background()); e != nil {
		t.Fatal(e)
	}
	got := make(chan error, 1)
	go func() { got <- l.acquire(context.Background()) }()
	select {
	case <-got:
		t.Fatal("acquired beyond the limit")
	case <-time.After(50 * time.Millisecond):
	}
	l.setLimit(2) // 上限を上げると、待機中の取得がすぐに通る。
	select {
	case e := <-got:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not woken by a higher limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := l.acquire(ctx); e == nil {
		t.Fatal("acquire ignored cancellation")
	}
}
