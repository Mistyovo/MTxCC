package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestReadRejectsPathEscape 验证文件工具的 root 约束：越界路径拒绝。
// 令其失败的生产改动：未做路径归一/前缀校验。
func TestReadRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := NewRead(root)

	if _, err := read.Call(context.Background(), mustJSON(t, map[string]string{"path": "../../etc/passwd"})); err == nil {
		t.Fatal("越界路径 ../../etc/passwd 应被拒绝")
	}
	if _, err := read.Call(context.Background(), mustJSON(t, map[string]string{"path": "/etc/passwd"})); err == nil {
		t.Fatal("绝对路径 /etc/passwd 应被拒绝")
	}
	out, err := read.Call(context.Background(), mustJSON(t, map[string]string{"path": "note.txt"}))
	if err != nil || out != "hi" {
		t.Fatalf("root 内读取 = (%q, %v), want (hi, nil)", out, err)
	}
}

// TestWriteListRoundtrip 验证 Write→List 往返（root 内）。
func TestWriteListRoundtrip(t *testing.T) {
	root := t.TempDir()
	write := NewWrite(root)
	list := NewList(root)

	if _, err := write.Call(context.Background(), mustJSON(t, map[string]any{"path": "sub/a.txt", "content": "A"})); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := write.Call(context.Background(), mustJSON(t, map[string]any{"path": "b.txt", "content": "B"})); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, err := list.Call(context.Background(), mustJSON(t, map[string]string{"path": "."}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !contains(out, "b.txt") || !contains(out, "sub/") {
		t.Fatalf("List(.) = %q", out)
	}
	outSub, err := list.Call(context.Background(), mustJSON(t, map[string]string{"path": "sub"}))
	if err != nil {
		t.Fatalf("List(sub): %v", err)
	}
	if !contains(outSub, "a.txt") {
		t.Fatalf("List(sub) 缺 a.txt: %q", outSub)
	}
	if _, err := write.Call(context.Background(), mustJSON(t, map[string]any{"path": "../evil", "content": "x"})); err == nil {
		t.Fatal("Write 越界应拒绝")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestRegistryDisable 验证 Disable 后 Get 不可见。
// 令其失败的生产改动：Disable 未生效。
func TestRegistryDisable(t *testing.T) {
	r := NewRegistry()
	read := NewRead(t.TempDir())
	r.Register(read)

	if _, ok := r.Get("read"); !ok {
		t.Fatal("注册后应可见")
	}
	r.Disable("read")
	if _, ok := r.Get("read"); ok {
		t.Fatal("Disable 后不应可见")
	}
	if got := r.List(); len(got) != 0 {
		t.Fatalf("List = %d, want 0", len(got))
	}
}
