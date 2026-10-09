package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"ljmdb/internal/backend"
	"ljmdb/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "启动或运行失败:", err)
		os.Exit(1)
	}
}
func run() error {
	c, err := config.Parse(os.Args[1:])
	if err != nil {
		return err
	}
	if c.ShowVersion {
		fmt.Println("OpenLJMDB", backend.Version)
		return nil
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("端口 %s 不可用，请通过 --port 选择其他端口: %w", addr, err)
	}
	defer listener.Close()
	a, err := backend.New(c.DataDir)
	if err != nil {
		return err
	}
	defer a.Close()
	if c.WebDir != "" {
		webDir, err := filepath.Abs(c.WebDir)
		if err != nil {
			return err
		}
		if info, err := os.Stat(filepath.Join(webDir, "index.html")); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("前端构建目录缺少 index.html")
		}
		a.FrontendFS = os.DirFS(webDir)
	}
	a.AllowedHosts = map[string]bool{addr: true, net.JoinHostPort("localhost", strconv.Itoa(c.Port)): true}
	srv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20, ErrorLog: log.New(os.Stderr, "http: ", log.LstdFlags)}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()
	address := "http://" + addr
	fmt.Printf("OpenLJMDB %s\nAPI: %s/api/v1\n数据目录: %s\n使用 Ctrl+C 正常退出后才能手工复制数据目录。\n", backend.Version, address, c.DataDir)
	if !c.NoOpen {
		if a.FrontendFS != nil {
			openBrowser(address)
		} else {
			openBrowser(address + "/api/v1/health")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err = <-errCh:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		srv.Close()
		return fmt.Errorf("等待在途请求超时: %w", err)
	}
	return a.Close()
}

func openBrowser(address string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", address)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "浏览器未能自动打开，请手动访问上方地址")
		return
	}
	go cmd.Wait()
}
