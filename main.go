package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"ruilian/internal/app"
	"ruilian/internal/branding"
	"ruilian/internal/licensecode"
	"ruilian/internal/platform"
)

//go:embed all:web/dist
var embeddedWeb embed.FS

func main() {
	if e := run(); e != nil {
		log.Print(e)
		quiet := false
		for _, arg := range os.Args[1:] {
			if arg == "--no-browser" || arg == "-no-browser" {
				quiet = true
			}
		}
		if !quiet {
			platform.Message(branding.Default.AppName+"启动失败", e.Error())
		}
		os.Exit(1)
	}
}
func run() error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	defaultData := filepath.Join(filepath.Dir(exe), "data")
	dataDir := flag.String("data-dir", defaultData, "SQLite 数据与备份目录")
	port := flag.Int("port", 17888, "本机 HTTP 端口；0 自动分配")
	noBrowser := flag.Bool("no-browser", false, "不自动打开浏览器")
	restore := flag.String("restore", "", "恢复指定 SQLite 备份（服务必须停止）")
	verify := flag.String("verify-backup", "", "仅校验 SQLite 备份文件")
	version := flag.Bool("version", false, "显示版本")
	flag.Parse()
	if *version {
		fmt.Println(branding.Default.AppName + " " + app.Version)
		return nil
	}
	if *verify != "" {
		hash, size, e := app.VerifyBackup(*verify)
		if e != nil {
			return e
		}
		fmt.Printf("校验通过\nSHA-256: %s\n大小: %d 字节\n", hash, size)
		return nil
	}
	*dataDir, e = filepath.Abs(*dataDir)
	if e != nil {
		return e
	}
	unlock, e := platform.Lock(*dataDir)
	if e != nil {
		if *restore != "" {
			return fmt.Errorf("恢复前请先停止本机服务。%w", e)
		}
		if !*noBrowser {
			if raw, er := os.ReadFile(filepath.Join(*dataDir, ".runtime.json")); er == nil {
				var runtime struct {
					URL string `json:"url"`
				}
				if json.Unmarshal(raw, &runtime) == nil && strings.HasPrefix(runtime.URL, "http://127.0.0.1:") {
					client := http.Client{Timeout: 2 * time.Second}
					resp, er := client.Get(runtime.URL + "/api/health")
					if er == nil {
						resp.Body.Close()
						if resp.StatusCode == 200 {
							_ = platform.Browser(runtime.URL)
							return nil
						}
					}
				}
			}
		}
		return e
	}
	defer unlock()
	if *restore != "" {
		saved, e := app.RestoreDatabase(*dataDir, *restore)
		if e != nil {
			return e
		}
		message := "数据库恢复完成。恢复前副本：" + saved + "。请重新启动软件并检查授权状态。"
		fmt.Println(message)
		if !*noBrowser {
			platform.Message("数据库恢复", message)
		}
		return nil
	}
	logs := filepath.Join(*dataDir, "logs")
	if e = os.MkdirAll(logs, 0700); e != nil {
		return e
	}
	file, e := os.OpenFile(filepath.Join(logs, "ruilian-"+time.Now().Format("2006-01-02")+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer file.Close()
	logger := log.New(file, "", log.LstdFlags|log.Lshortfile)
	a, e := app.Open(*dataDir, licensecode.PublicKeyPEM, logger)
	if e != nil {
		return fmt.Errorf("无法打开数据库：%w\n数据目录：%s", e, *dataDir)
	}
	defer a.Close()
	if e = a.EnsureDefaultAccount(); e != nil {
		return fmt.Errorf("初始化默认管理员失败：%w", e)
	}
	a.DesktopToken = os.Getenv("RUILIAN_DESKTOP_TOKEN")
	listener, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if e != nil && *port == 17888 {
		listener, e = net.Listen("tcp", "127.0.0.1:0")
	}
	if e != nil {
		return e
	}
	defer listener.Close()
	address := "http://" + listener.Addr().String()
	assets, e := fs.Sub(embeddedWeb, "web/dist")
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Stop = cancel
	server := &http.Server{Handler: a.Router(assets), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 16}
	runtimeFile := filepath.Join(*dataDir, ".runtime.json")
	raw, _ := json.Marshal(map[string]any{"url": address, "pid": os.Getpid(), "version": app.Version})
	if e = os.WriteFile(runtimeFile, raw, 0600); e != nil {
		return e
	}
	defer os.Remove(runtimeFile)
	backgroundDone := make(chan struct{})
	go func() { defer close(backgroundDone); a.Background(ctx) }()
	errors := make(chan error, 1)
	go func() { errors <- server.Serve(listener) }()
	logger.Printf("%s %s 启动于 %s，SQLite: %s", a.Branding().AppName, app.Version, address, *dataDir)
	fmt.Println(address)
	if !*noBrowser {
		if e = platform.Browser(address); e != nil {
			logger.Printf("打开浏览器失败: %v", e)
			platform.Message("数据服务已启动", "请在浏览器打开："+address)
		}
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	select {
	case <-ctx.Done():
	case <-sig:
		cancel()
	case e = <-errors:
		if e != nil && e != http.ErrServerClosed {
			return e
		}
	}
	shutdown, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	logger.Print("正在安全停止服务")
	cancel()
	e = server.Shutdown(shutdown)
	select {
	case <-backgroundDone:
	case <-shutdown.Done():
	}
	return e
}
