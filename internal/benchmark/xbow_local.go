package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// XBowLocal 是 XBOW Validation Benchmark 本地模式 adapter
//（实证：PurpleAILAB/xbow-validation-benchmarks 开源仓库，无需 token）。
// 题目目录结构：benchmarks/XBEN-xxx-24/{.env(FLAG), Makefile, README.md, docker-compose.yml, app/, mysql/}。
type XBowLocal struct {
	BenchDir string  // 仓库根或 benchmarks 目录
	Exec     CmdExec // 命令执行器（测试注入 fakeExec）
}

// NewXBowLocal 建本地 adapter。
func NewXBowLocal(benchDir string) *XBowLocal { return &XBowLocal{BenchDir: benchDir} }

// Name 见 Adapter。
func (a *XBowLocal) Name() string { return "xbow-local" }

// challengeDir 定位题目目录（兼容 BenchDir 直指 benchmarks/ 或仓库根）。
func (a *XBowLocal) challengeDir(id string) string {
	for _, base := range []string{
		filepath.Join(a.BenchDir, id),
		filepath.Join(a.BenchDir, "benchmarks", id),
	} {
		if st, err := os.Stat(base); err == nil && st.IsDir() {
			return base
		}
	}
	return filepath.Join(a.BenchDir, id)
}

// ListChallenges 扫描题目目录（XBEN-*），从 README 抽标题与难度。
func (a *XBowLocal) ListChallenges() ([]Challenge, error) {
	var out []Challenge
	candidates := []string{a.BenchDir, filepath.Join(a.BenchDir, "benchmarks")}
	for _, base := range candidates {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "XBEN-") {
				continue
			}
			c := Challenge{ID: e.Name(), Title: e.Name(), Difficulty: "unknown"}
			if b, err := os.ReadFile(filepath.Join(base, e.Name(), "README.md")); err == nil {
				lines := strings.SplitN(string(b), "\n", 3)
				if len(lines) > 0 {
					c.Title = strings.TrimSpace(strings.TrimLeft(lines[0], "# "))
				}
				if m := regexp.MustCompile(`Difficulty\s*\n+(\w+)`).FindStringSubmatch(string(b)); m != nil {
					c.Difficulty = m[1]
				}
			}
			out = append(out, c)
		}
		if len(out) > 0 {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未找到题目目录（%s 下无 XBEN-*）", a.BenchDir)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// flagRE 匹配 .env 里的 FLAG 值。
var flagRE = regexp.MustCompile(`FLAG="?([^\s"]+)"?`)

// FlagOf 读题目 .env 注入的 flag（构建期值）。
func (a *XBowLocal) FlagOf(id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(a.challengeDir(id), ".env"))
	if err != nil {
		return "", err
	}
	if m := flagRE.FindStringSubmatch(string(b)); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("%s/.env 无 FLAG", id)
}

// CheckFlag 校验提交的 flag。
func (a *XBowLocal) CheckFlag(id, flag string) bool {
	want, err := a.FlagOf(id)
	if err != nil {
		return false
	}
	return strings.TrimSpace(flag) == want
}

// StartChallenge 构建并起靶场（docker compose build --build-arg flag= → up -d --wait），
// 返回访问 URL。
func (a *XBowLocal) StartChallenge(id string) (string, error) {
	dir := a.challengeDir(id)
	e := NewComposeExecutor(dir)
	if a.Exec != nil {
		e.Exec = a.Exec
	}
	flag, err := a.FlagOf(id)
	if err != nil {
		flag = "flag{" + randomHex16() + "}"
	}
	if _, err := e.Build(flag); err != nil {
		return "", fmt.Errorf("build: %w", err)
	}
	if _, err := e.Up(); err != nil {
		return "", fmt.Errorf("up: %w", err)
	}
	port, err := e.Port()
	if err != nil {
		return "", fmt.Errorf("port: %w", err)
	}
	return "http://127.0.0.1:" + port, nil
}

// StopChallenge 停止并清理靶场。
func (a *XBowLocal) StopChallenge(id string) error {
	e := NewComposeExecutor(a.challengeDir(id))
	if a.Exec != nil {
		e.Exec = a.Exec
	}
	_, err := e.Down()
	return err
}

// randomHex16 随机 flag 后缀。
func randomHex16() string {
	b := make([]byte, 8)
	for i := range b {
		b[i] = "0123456789abcdef"[int(time.Now().UnixNano()+int64(i))%16]
	}
	return string(b)
}
