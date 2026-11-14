package download

import (
	"context"
	"sync"
)

// limiter は、上限を実行中に変更できるセマフォ。
// 上限を下げても実行中の処理は止めず、空きができるまで新規の取得を待たせる。
type limiter struct {
	mu    sync.Mutex
	limit int
	used  int
	wake  chan struct{}
}

func newLimiter(limit int) *limiter { return &limiter{limit: limit, wake: make(chan struct{})} }

// 呼び出し元が l.mu を保持する。待機中の全 goroutine を起こす。
func (l *limiter) broadcast() {
	close(l.wake)
	l.wake = make(chan struct{})
}
func (l *limiter) acquire(ctx context.Context) error {
	for {
		l.mu.Lock()
		if l.used < l.limit {
			l.used++
			l.mu.Unlock()
			return nil
		}
		wake := l.wake
		l.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (l *limiter) release() {
	l.mu.Lock()
	l.used--
	l.broadcast()
	l.mu.Unlock()
}
func (l *limiter) setLimit(n int) {
	l.mu.Lock()
	l.limit = n
	l.broadcast()
	l.mu.Unlock()
}
func (l *limiter) get() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}
