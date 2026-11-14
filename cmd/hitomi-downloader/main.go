package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"hitomi-downloader/internal/download"
	"hitomi-downloader/internal/hitomi"
	"hitomi-downloader/internal/server"
)

func main() {
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
func run() error {
	port := flag.Int("port", 0, "本地 HTTP 端口，0 为自动分配")
	state := flag.String("data-dir", "./data", "配置和任务记录目录")
	root := flag.String("download-dir", "", "下载目录（覆盖已保存设置）")
	proxy := flag.String("proxy", "", "Proxy URL；direct 禁用 Proxy；默认使用环境变量")
	noOpen := flag.Bool("no-open", false, "不自动打开浏览器")
	flag.Parse()
	statePath, e := filepath.Abs(*state)
	if e != nil {
		return e
	}
	configPath := filepath.Join(statePath, "config.json")
	if *root == "" {
		b, e := os.ReadFile(configPath)
		if e == nil {
			var c server.Config
			if e = json.Unmarshal(b, &c); e != nil {
				return fmt.Errorf("配置损坏：%w", e)
			}
			*root = c.DownloadDir
		} else if !os.IsNotExist(e) {
			return e
		}
		if *root == "" {
			*root = "./downloads"
		}
	}
	client, e := hitomi.New(*proxy)
	if e != nil {
		return e
	}
	manager, e := download.New(client, *root, statePath)
	if e != nil {
		return e
	}
	defer manager.Close()
	app := server.New(client, manager, statePath)
	listener, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if e != nil {
		return e
	}
	url := "http://" + listener.Addr().String() + "/?token=" + app.Token
	fmt.Println("Hitomi Local:", url)
	fmt.Println("下载目录:", manager.Root())
	if !*noOpen {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		if e := cmd.Start(); e == nil {
			go func() { _ = cmd.Wait() }()
		} else {
			log.Printf("请手动打开启动链接：%v", e)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, listener, app)
}

func serve(ctx context.Context, listener net.Listener, app *server.Server) error {
	requests, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	srv := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return requests }}
	defer srv.Close()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	select {
	case e := <-done:
		cancelRequests()
		if e != http.ErrServerClosed {
			return e
		}
	case <-ctx.Done():
	case <-app.ShutdownRequested():
	}
	app.RequestShutdown()
	cancelRequests()
	// HTTP の排出待ちと並行して worker を停止し、進捗の保存完了を待つ。
	downloadsDone := make(chan struct{})
	go func() { app.Downloads.Close(); close(downloadsDone) }()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := srv.Shutdown(shutdown); e != nil {
		_ = srv.Close()
		return fmt.Errorf("等待 HTTP 连接关闭失败：%w", e)
	}
	<-downloadsDone
	return nil
}
