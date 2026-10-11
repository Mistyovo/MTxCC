package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMCPServer 是一个 stdio JSON-RPC echo 工具服务器（python3 实现）。
const fakeServer = `#!/usr/bin/env python3
import json, sys
def send(o): sys.stdout.write(json.dumps(o)+"\n"); sys.stdout.flush()
for line in sys.stdin:
    try: m = json.loads(line)
    except: continue
    if m.get("method") == "initialize":
        send({"jsonrpc":"2.0","id":m["id"],"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"fake"}}})
    elif m.get("method") == "notifications/initialized":
        pass
    elif m.get("method") == "tools/list":
        send({"jsonrpc":"2.0","id":m["id"],"result":{"tools":[{"name":"echo","description":"回显","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]}})
    elif m.get("method") == "tools/call":
        send({"jsonrpc":"2.0","id":m["id"],"result":{"content":[{"type":"text","text":"ECHO:"+m["params"]["arguments"]["text"]}]}})
`

// TestStdioClientLifecycle 假服务器：initialize → tools/list → tools/call 全链。
func TestStdioClientLifecycle(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake_mcp.py")
	if err := os.WriteFile(script, []byte(fakeServer), 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := NewStdioClient("python3", script)
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[0].InputSchema == nil {
		t.Fatal("schema 应带回")
	}

	out, err := c.CallTool(context.Background(), "echo", map[string]any{"text": "你好"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(out, "ECHO:你好") {
		t.Fatalf("out = %q", out)
	}
}

// TestDeferredNaming deferred 工具命名规范 mcp__<server>__<tool>。
func TestDeferredNaming(t *testing.T) {
	if got := ToolName("srv1", "echo"); got != "mcp__srv1__echo" {
		t.Fatalf("ToolName = %q", got)
	}
	if s, tl, ok := SplitToolName("mcp__srv1__echo"); !ok || s != "srv1" || tl != "echo" {
		t.Fatalf("SplitToolName = %q,%q,%v", s, tl, ok)
	}
	if _, _, ok := SplitToolName("bash"); ok {
		t.Fatal("非 mcp 工具应返回 false")
	}
}
