package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"hitomi-downloader/internal/download"
	"hitomi-downloader/internal/hitomi"
	"hitomi-downloader/internal/server"
)

// TCP ポートを使わず、実際の HTTP 接続と終了処理を検証する。
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080} }
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, backend := net.Pipe()
	select {
	case l.conns <- backend:
		return client, nil
	case <-ctx.Done():
		client.Close()
		backend.Close()
		return nil, ctx.Err()
	case <-l.closed:
		client.Close()
		backend.Close()
		return nil, net.ErrClosed
	}
}

func TestGracefulShutdownWithLiveSSE(t *testing.T) {
	for _, fromButton := range []bool{false, true} {
		name := "signal"
		if fromButton {
			name = "button"
		}
		t.Run(name, func(t *testing.T) {
			client, _ := hitomi.New("direct")
			manager, e := download.New(client, t.TempDir(), t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer manager.Close()
			app := server.New(client, manager, t.TempDir())
			listener := &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- serve(ctx, listener, app) }()
			transport := &http.Transport{DialContext: listener.dial}
			defer transport.CloseIdleConnections()
			httpClient := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			request := func(method, path string) *http.Request {
				r, _ := http.NewRequest(method, "http://127.0.0.1:8080"+path, nil)
				r.AddCookie(&http.Cookie{Name: "hitomi_session", Value: app.Token})
				return r
			}
			response, e := httpClient.Do(request("GET", "/api/v1/events"))
			if e != nil {
				t.Fatal(e)
			}
			defer response.Body.Close()
			reader := bufio.NewReader(response.Body)
			line, e := reader.ReadString('\n')
			if e != nil || !strings.HasPrefix(line, "data:") {
				t.Fatalf("SSE failed: %q %v", line, e)
			}
			start := time.Now()
			if fromButton {
				res, e := httpClient.Do(request("POST", "/api/v1/shutdown"))
				if e != nil {
					t.Fatal(e)
				}
				body, e := io.ReadAll(res.Body)
				res.Body.Close()
				if e != nil || res.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
					t.Fatalf("shutdown response %s %v", body, e)
				}
			} else {
				cancel()
			}
			rest, e := io.ReadAll(reader)
			if e != nil || !strings.Contains(string(rest), "event: shutdown") {
				t.Fatalf("missing terminal event: %s %v", rest, e)
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown blocked by SSE")
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("shutdown exceeded expected drain duration")
			}
			if e := manager.Start(42, "", "download"); e == nil {
				t.Fatal("accepted task after shutdown")
			}
		})
	}
}
