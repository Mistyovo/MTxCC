// MTxCC — AI 自主渗透测试系统（仅限授权测试场景使用）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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

	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("MTxCC v0.3.0-m2")
		if cfg, err := config.Load(); err == nil {
			if st, err := server.New(server.Options{
				DatabaseURL: cfg.DatabaseURL, MasterKey: cfg.MasterKey, DataDir: cfg.DataDir,
			}); err == nil {
				n, _ := st.SchemaVersion(context.Background())
				fmt.Printf("schema: v%d\n", n)
				st.Close()
			}
		}
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.ListenAddr = *addr
	}

	srv, err := server.New(server.Options{
		DatabaseURL:  cfg.DatabaseURL,
		MasterKey:    cfg.MasterKey,
		DataDir:      cfg.DataDir,
		SandboxImage: os.Getenv("MTX_SANDBOX_IMAGE"),
	})
	if err != nil {
		return err
	}
	defer srv.Close()
	srv.RecoverRunning()

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	log.Printf("MTxCC v0.3.0 监听 %s（API + 内嵌前端）", cfg.ListenAddr)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
	return nil
}
