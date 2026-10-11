//go:build docker

package e2e

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallScriptDryRun 验证安装脚本 DRYRUN 路径：
// .env 生成（主密钥为 32 字节 base64、密码无特殊字符）、幂等重跑不覆盖。
func TestInstallScriptDryRun(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("无 docker，跳过")
	}
	dir := t.TempDir()
	script, err := filepath.Abs(filepath.Join("..", "..", "deploy", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}

	run := func() {
		cmd := exec.Command("bash", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "MTX_DIR="+dir, "MTX_INSTALL_DRYRUN=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install.sh DRYRUN: %v\n%s", err, out)
		}
	}

	run()
	b, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf(".env 未生成: %v", err)
	}
	env := string(b)
	if !strings.Contains(env, "MTX_MASTER_KEY=") || !strings.Contains(env, "MTX_PG_PASSWORD=") {
		t.Fatalf(".env 字段缺失:\n%s", env)
	}
	// 主密钥解码后 32 字节。
	for _, line := range strings.Split(env, "\n") {
		if strings.HasPrefix(line, "MTX_MASTER_KEY=") {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "MTX_MASTER_KEY="))
			if err != nil || len(raw) != 32 {
				t.Fatalf("主密钥应为 32 字节 base64: %d 字节, err=%v", len(raw), err)
			}
		}
	}

	// 幂等：重跑不覆盖既有值。
	first := env
	run()
	b2, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if string(b2) != first {
		t.Fatal("重跑覆盖了 .env（幂等性破坏）")
	}
}

// TestComposeConfigValid 验证 compose 文件语法与 demo profile。
func TestComposeConfigValid(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("无 docker，跳过")
	}
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command("docker", "compose", "-f", "deploy/docker-compose.yml", "--env-file", "/dev/null",
		"--profile", "demo", "config", "--quiet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MTX_PG_PASSWORD=testpw", "MTX_MASTER_KEY=dGVzdA==")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compose config 无效: %v\n%s", err, out)
	}
}
