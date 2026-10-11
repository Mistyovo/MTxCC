package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mtx/internal/harness"
	"mtx/internal/llm"
	"mtx/internal/tools"
)

// expertMaxTurns 是专家小循环预算（深度 1，防递归爆炸）。
const expertMaxTurns = 12

// expertHTTPPrompt / expertWritePoCPrompt 是专家 system（代码常量）。
const expertHTTPPrompt = `你是渗透测试团队的 HTTP 专家（expert agent）。
输入是一个单点目标（objective）。你用 bash 里的 curl 完成这一件事：
构造并发送所需的 HTTP 请求（可多看响应调整一次），然后输出简洁结论。
不要展开侦察、不要写文件。结论以 "EXPERT_DONE:" 开头。`

const expertWritePoCPrompt = `你是渗透测试团队的 PoC 编写专家（expert agent）。
输入是漏洞描述与复现要点。你写一个最小可复现的 PoC 脚本到工作区（write 工具），
用 bash 自检语法（如 python -m py_compile / bash -n），然后输出脚本路径与用法。
结论以 "EXPERT_DONE:" 开头。`

// ExpertTools 生成专家委托工具（深度 1；Depth>0 返回空——防递归，Review Focus 1）。
func ExpertTools(deps CommonDeps, ec ExecContext) []tools.Tool {
	if ec.Depth > 0 {
		return nil
	}
	return []tools.Tool{
		&expertTool{deps: deps, ec: ec, kind: "http"},
		&expertTool{deps: deps, ec: ec, kind: "poc"},
	}
}

type expertTool struct {
	deps CommonDeps
	ec   ExecContext
	kind string // http | poc
}

func (t *expertTool) Name() string {
	if t.kind == "http" {
		return "expert_http"
	}
	return "expert_write_poc"
}

func (t *expertTool) Description() string {
	if t.kind == "http" {
		return `委托 HTTP 专家完成一个单点请求目标：{objective}。
例如"用 POST /login 验证 admin:123456 是否有效并给出结论"。返回专家结论文本。`
	}
	return `委托 PoC 专家写最小复现脚本：{objective, vuln_class}。
返回脚本路径与用法。专家不能展开侦察。`
}

func (t *expertTool) Schema() map[string]any {
	if t.kind == "http" {
		return map[string]any{"type": "object", "properties": map[string]any{
			"objective": map[string]any{"type": "string"},
		}, "required": []string{"objective"}}
	}
	return map[string]any{"type": "object", "properties": map[string]any{
		"objective":  map[string]any{"type": "string"},
		"vuln_class": map[string]any{"type": "string"},
	}, "required": []string{"objective"}}
}

func (t *expertTool) Flags() tools.Flags { return tools.Flags{} }
func (t *expertTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *expertTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Objective string `json:"objective"`
		VulnClass string `json:"vuln_class"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Objective) == "" {
		return "", fmt.Errorf("objective 必填")
	}

	// 专家会话工具面：bash + 文件三件套（无领域写回、无 expert——深度自然受限）。
	reg := tools.NewRegistry()
	reg.Register(tools.NewBash(t.deps.Bash))
	root := t.deps.Workroot
	if root == "" {
		root = "."
	}
	reg.Register(tools.NewRead(root))
	reg.Register(tools.NewWrite(root))
	reg.Register(tools.NewList(root))

	sys := expertHTTPPrompt
	if t.kind == "poc" {
		sys = expertWritePoCPrompt
	}
	opts := harness.Options{
		Tools:      reg,
		System:     sys,
		MaxTurns:   expertMaxTurns,
		MaxTokens:  orDefault(t.deps.MaxTokens, 8192),
		Model:      t.deps.Model,
		Transcript: t.deps.Transcript,
	}
	opts.Provider = providerOf(t.deps.Provider)
	if opts.Provider == nil {
		return "", fmt.Errorf("expert 委托需要 LLM provider")
	}
	sess := harness.NewSession(opts, fmt.Sprintf("t%d-expert-%s", t.ec.TaskID, t.kind))
	res, err := sess.Query(ctx, sys, "单点目标："+in.Objective)
	if err != nil {
		return "", fmt.Errorf("专家会话失败: %w", err)
	}
	return res.FinalText, nil
}

// orDefault 取默认值。
func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// providerOf 从 CommonDeps.Provider（interface{}）取 llm.Provider（类型断言）。
func providerOf(p any) llm.Provider {
	if prov, ok := p.(llm.Provider); ok {
		return prov
	}
	return nil
}
