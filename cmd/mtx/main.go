// MTxCC — AI 自主渗透测试系统（仅限授权测试场景使用）。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"mtx/internal/config"
	"mtx/internal/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	addr := flag.String("addr", "", "HTTP 监听地址（覆盖 MTX_LISTEN_ADDR）")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.ListenAddr = *addr
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.NewRouter(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Printf("MTxCC 监听 %s", cfg.ListenAddr)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		// 优雅退出：SIGTERM → 5s 排空窗口。
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}
