package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestComposeExecutorCommand 构造 docker compose 命令（FLAG 注入 + healthcheck 等待）：
// 纯逻辑断言（不真跑 docker），保证 StartChallenge 的命令形态正确。
func TestComposeExecutorCommand(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}"), 0o644)

	e := NewComposeExecutor(dir)
	build := e.buildCmd("flag{abc}")
	joined := strings.Join(build.Args, " ")
	if !strings.Contains(joined, "compose build") || !strings.Contains(joined, "--build-arg flag=flag{abc}") {
		t.Fatalf("build 命令形态错: %v", build.Args)
	}
	up := e.upCmd()
	joinedUp := strings.Join(up.Args, " ")
	if !strings.Contains(joinedUp, "compose up -d --wait") {
		t.Fatalf("up 命令形态错: %v", up.Args)
	}
	down := e.downCmd()
	if !strings.Contains(strings.Join(down.Args, " "), "compose down") {
		t.Fatalf("down 命令形态错: %v", down.Args)
	}
	// 端口探测命令。
	ps := e.portCmd()
	if !strings.Contains(strings.Join(ps.Args, " "), "compose ps") {
		t.Fatalf("port 探测命令形态错: %v", ps.Args)
	}
}

// TestXBowLocalStart 测试 StartChallenge 集成形态（mock exec）。
func TestXBowLocalStartWithExecutor(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "XBEN-001-24"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "XBEN-001-24", "docker-compose.yml"), []byte("services: {}"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "XBEN-001-24", ".env"), []byte(`FLAG="flag{x}"`), 0o644)

	fake := &fakeExec{portOutput: "0.0.0.0:32768\n"}
	a := NewXBowLocal(dir)
	a.Exec = fake
	url, err := a.StartChallenge("XBEN-001-24")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://127.0.0.1:32768" {
		t.Fatalf("url = %s", url)
	}
	// 确认 build 带了 flag、up、port 被调用。
	if !fake.called("build") || !fake.called("up") || !fake.called("ps") {
		t.Fatalf("命令调用不全: %v", fake.calls)
	}
}
