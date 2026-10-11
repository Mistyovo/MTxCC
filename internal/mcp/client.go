// Package mcp 实现 MCP（Model Context Protocol）stdio 客户端（spec §6.3）：
// JSON-RPC 2.0 子进程；deferred 命名 mcp__<server>__<tool>。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// ToolDef 是发现到的工具（schema 保留供执行前校验）。
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// StdioClient 是一个 MCP 服务器的客户端连接。
type StdioClient struct {
	cmd    *exec.Cmd
	stdin  chan string
	mu     sync.Mutex
	nextID int
	// 响应按 id 分发。
	pending sync.Map // id -> chan json.RawMessage
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// NewStdioClient 启动子进程并完成 initialize 握手。
func NewStdioClient(argv ...string) (*StdioClient, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("argv 为空")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &StdioClient{cmd: cmd}
	c.stdin = make(chan string, 64)

	// 读循环：按 id 分发响应。
	go func() {
		sc := bufio.NewScanner(stdoutPipe)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var m rpcMsg
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil || m.ID == 0 {
				continue // 通知/噪声
			}
			if ch, ok := c.pending.Load(m.ID); ok {
				ch.(chan json.RawMessage) <- m.Result
				c.pending.Delete(m.ID)
			}
		}
	}()
	go func() {
		for line := range c.stdin {
			if _, err := stdinPipe.Write([]byte(line + "\n")); err != nil {
				return
			}
		}
	}()

	// initialize 握手。
	ctx := context.Background()
	var initRes json.RawMessage
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mtx", "version": "0.2"},
	}, &initRes); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	c.notify("notifications/initialized")
	return c, nil
}

// Close 结束子进程。
func (c *StdioClient) Close() {
	close(c.stdin)
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// ListTools 拉取工具清单。
func (c *StdioClient) ListTools(ctx context.Context) ([]ToolDef, error) {
	var res struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	out := make([]ToolDef, 0, len(res.Tools))
	for _, t := range res.Tools {
		out = append(out, ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out, nil
}

// CallTool 调用工具并返回文本结果拼接。
func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return "", err
	}
	if res.IsError {
		return "", fmt.Errorf("工具 %s 返回错误", name)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

// call 发请求等响应。
func (c *StdioClient) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	ch := make(chan json.RawMessage, 1)
	c.pending.Store(id, ch)

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case c.stdin <- string(b):
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case res := <-ch:
		return json.Unmarshal(res, out)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *StdioClient) notify(method string) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	select {
	case c.stdin <- string(b):
	default:
	}
}

// ToolName 生成 deferred 命名。
func ToolName(server, tool string) string {
	return "mcp__" + server + "__" + tool
}

// SplitToolName 反解 deferred 命名。
func SplitToolName(name string) (server, tool string, ok bool) {
	if !strings.HasPrefix(name, "mcp__") {
		return "", "", false
	}
	rest := strings.TrimPrefix(name, "mcp__")
	i := strings.Index(rest, "__")
	if i <= 0 {
		return "", "", false
	}
	return rest[:i], rest[i+2:], true
}
