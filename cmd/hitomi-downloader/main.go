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
	taskConc := flag.Int("task-concurrency", 0, "同时处理的任务数（覆盖已保存设置，默认 2）")
	pageConc := flag.Int("page-concurrency", 0, "每个任务的并发文件数（覆盖已保存设置，默认 4）")
	lan := flag.Bool("lan", false, "允许同一局域网的手机打开只读的阅读页（/reader）；默认只监听本机")
	noOpen := flag.Bool("no-open", false, "不自动打开浏览器")
	flag.Parse()
	statePath, e := filepath.Abs(*state)
	if e != nil {
		return e
	}
	configPath := filepath.Join(statePath, "config.json")
	// 保存済みの設定を読み、CLI 引数で指定された項目だけを上書きする。
	var saved server.Config
	if b, e := os.ReadFile(configPath); e == nil {
		if e = json.Unmarshal(b, &saved); e != nil {
			return fmt.Errorf("配置损坏：%w", e)
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if *root == "" {
		*root = saved.DownloadDir
	}
	if *root == "" {
		*root = "./downloads"
	}
	if *taskConc == 0 {
		*taskConc = saved.TaskConcurrency
	}
	if *pageConc == 0 {
		*pageConc = saved.PageConcurrency
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
	if e := manager.SetConcurrency(*taskConc, *pageConc); e != nil {
		return e
	}
	app := server.New(client, manager, statePath)
	bind := "127.0.0.1"
	if *lan {
		bind = "0.0.0.0"
	}
	listener, e := net.Listen("tcp", fmt.Sprintf("%s:%d", bind, *port))
	if e != nil {
		return e
	}
	app.LAN = *lan
	app.Addr = listener.Addr().String()
	listenPort := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", listenPort, app.Token)
	fmt.Println("Hitomi Local:", url)
	fmt.Println("下载目录:", manager.Root())
	if *lan {
		fmt.Println("手机阅读（只读，需在同一局域网）：")
		for _, link := range app.ReaderLinks() {
			fmt.Println("  ", link)
		}
	} else {
		fmt.Println("手机阅读：以 --lan 启动后，可在 GUI 的“设置”中获取手机链接")
	}
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
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if e := srv.Shutdown(shutdown); e != nil {
		// ブラウザが先行して開いた接続（まだリクエストのない接続）は、Shutdown では 5 秒間アイドル扱いにならず待たされる。
		// 進行中の処理はキャンセル済みで、進捗の保存は下で待つため、残りの接続は強制的に閉じてよい。
		_ = srv.Close()
	}
	<-downloadsDone
	return nil
}
