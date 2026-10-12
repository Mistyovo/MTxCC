package guard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mtx/internal/roe"
)

// TestScriptIndirectTargetScan 脚本间接目标扫描（实证 gap：python 脚本内目标
// 绕过解析层）：bash 执行脚本文件时，扫描脚本内容提取目标过 RoE。
func TestScriptIndirectTargetScan(t *testing.T) {
	dir := t.TempDir()
	// 脚本内含越权目标。
	script := filepath.Join(dir, "probe.py")
	_ = os.WriteFile(script, []byte("import socket\nsocket.connect(('172.16.0.9', 80))\n"), 0o644)
	// 合法脚本。
	ok := filepath.Join(dir, "ok.py")
	_ = os.WriteFile(ok, []byte("print('hello')\n"), 0o644)

	r := roe.Rules{Mode: "enforce", InScope: []string{"127.0.0.1:32771"}}
	// 越权脚本执行 → 拒。
	d := roe.EvaluateCommand("python3 "+script, r)
	if d.Allow {
		t.Fatalf("脚本内越权目标应被拒: %+v", d)
	}
	// 合法脚本 → 放行。
	if d := roe.EvaluateCommand("python3 "+ok, r); !d.Allow {
		t.Fatalf("合法脚本被误拒: %+v", d)
	}
	_ = context.Background()
	_ = json.RawMessage("{}")
}
