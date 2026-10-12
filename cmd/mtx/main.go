// MTxCC — AI 自主渗透测试系统（仅限授权测试场景使用）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mtx/internal/benchmark"
	"mtx/internal/config"
	"mtx/internal/server"
)

// runBench mtx bench --adapter dvwa --api http://127.0.0.1:8080 --token <jwt> [--model 名称]
func runBench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	adapter := fs.String("adapter", "dvwa", "基准平台：dvwa|xbow（本地仓库，MTX_XBOW_DIR）|tsec")
	api := fs.String("api", "http://127.0.0.1:8080", "MTxCC API 地址")
	token := fs.String("token", os.Getenv("MTX_TOKEN"), "MTxCC JWT")
	model := fs.String("model", "auto", "模型名（报告标注用）")
	timeout := fs.Int("timeout", 0, "单题超时（秒），0=无限（默认）")
	_ = fs.Parse(args)

	reg := benchmark.NewRegistry()
	reg.Register("dvwa", &benchmark.DVWAAdapter{})
	reg.Register("xbow", benchmark.NewXBowLocal(os.Getenv("MTX_XBOW_DIR"))) // 本地仓库模式（无需 token）
	reg.Register("tsec", &benchmark.TsecAdapter{Token: os.Getenv("MTX_TSEC_TOKEN")})
	a, ok := reg.Get(*adapter)
	if !ok {
		return fmt.Errorf("未知 adapter %q", *adapter)
	}
	if *token == "" {
		return fmt.Errorf("缺少 --token（或 MTX_TOKEN 环境变量）")
	}

	to := *timeout
	if to == 0 {
		to = 86400
	}
	r := &benchmark.Runner{Adapter: a, APIBase: *api, Token: *token, Model: *model, TimeoutSec: to}
	fmt.Printf("▶ 基准跑分：adapter=%s api=%s model=%s\n", *adapter, *api, *model)
	rep, err := r.RunSuite()
	if err != nil {
		return err
	}

	fmt.Printf("\n=== 成绩 ===\n题目 %d · 解出 %d · 成功率 %.1f%% · 平均时延 %ds\n",
		rep.Total, rep.Solved, rep.SuccessRate(), rep.AvgLatencySec)
	for _, res := range r.Results {
		mark := "✗"
		if res.Solved {
			mark = "✓"
		}
		detail := ""
		if res.Err != "" {
			detail = " — " + res.Err
		} else {
			detail = fmt.Sprintf("（findings=%d）", res.Findings)
		}
		fmt.Printf("  %s %s %s%s\n", mark, res.ID, res.Title, detail)
	}
	fmt.Printf("\n=== 与参考系对比 ===\n")
	for k, c := range rep.CompareWithReference() {
		fmt.Printf("  %s: %.1f%% vs 我们 %.1f%%（差 %+.1f）\n",
			k, c["rate"].(float64), c["ours"].(float64), c["diff"].(float64))
	}

	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Printf("\n%s\n", out)
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	addr := flag.String("addr", "", "HTTP 监听地址（覆盖 MTX_LISTEN_ADDR）")
	flag.Parse()

	if len(os.Args) > 1 && os.Args[1] == "bench" {
		return runBench(os.Args[2:])
	}

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
