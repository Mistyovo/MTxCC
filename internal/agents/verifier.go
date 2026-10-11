package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"mtx/internal/harness"
	"mtx/internal/llm"
	"mtx/internal/tools"
)

// VerifierPrompt 是反证验证者 system（spec §5.1 verifier 角色）。
const VerifierPrompt = `你是渗透测试的"独立验证者"（verifier agent）。给你一个已上报的漏洞发现，
你的职责是**尝试证伪它**，而不是复述它。

规则：
1. 先读原始证据，再亲手复测（bash 只读命令）：重放请求、比对特征。
2. 结论走 submit_verdict 单出口：confirmed 必须 has_retest=true（你真实复测过）；
   无法复现 → confirmed=false 并写明差异。
3. 你没有写回工具；不得扩大测试范围。
4. 结论先于行动：如果证据链自洽且复测吻合，快速确认即可，不展开新探测。`

// VerdictIn 是 submit_verdict 参数。
type VerdictIn struct {
	Confirmed bool   `json:"confirmed"`
	HasRetest bool   `json:"has_retest"`
	Notes     string `json:"notes"`
}

// verdictTool 实现 hasRetest 硬门。
type verdictTool struct {
	mu      chan struct{}
	last    *VerdictIn
}

func newVerifierVerdictTool() *verdictTool {
	return &verdictTool{mu: make(chan struct{}, 1)}
}

// validate 是 hasRetest 门（Review Focus 5）。
func (t *verdictTool) validate(v VerdictIn) error {
	if v.Confirmed && !v.HasRetest {
		return fmt.Errorf("confirmed 必须附带真实复测（has_retest=true）——没有重放证据不得确认")
	}
	return nil
}

func (t *verdictTool) Name() string        { return "submit_verdict" }
func (t *verdictTool) Description() string { return "提交验证结论（单出口）：{confirmed, has_retest, notes}。" }
func (t *verdictTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"confirmed":  map[string]any{"type": "boolean"},
		"has_retest": map[string]any{"type": "boolean"},
		"notes":      map[string]any{"type": "string"},
	}, "required": []string{"confirmed", "has_retest"}}
}
func (t *verdictTool) Flags() tools.Flags                                { return tools.Flags{} }
func (t *verdictTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *verdictTool) Call(_ context.Context, input json.RawMessage) (string, error) {
	var v VerdictIn
	if err := json.Unmarshal(input, &v); err != nil {
		return "", err
	}
	if err := t.validate(v); err != nil {
		return "", err
	}
	t.mu <- struct{}{}
	t.last = &v
	<-t.mu
	out, _ := json.Marshal(v)
	return string(out), nil
}

// LastVerdict 返回最近结论（引擎读取写回 findings）。
func (t *verdictTool) LastVerdict() *VerdictIn {
	t.mu <- struct{}{}
	defer func() { <-t.mu }()
	return t.last
}

// VerifierToolRegistry 建 verifier 工具面：bash（只读复测）+ 文件读 + submit_verdict 单出口。
func VerifierToolRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	reg.Register(newVerifierVerdictTool())
	return reg
}

// AssembleVerifier 装配 verifier 会话（bash 注入由调用方补——工具面在 runVerifierSession 内组）。
func AssembleVerifier(bashExec tools.BashExec, model string, maxTokens int) harness.Options {
	reg := tools.NewRegistry()
	if bashExec != nil {
		reg.Register(tools.NewBash(bashExec))
	}
	reg.Register(newVerifierVerdictTool())
	return harness.Options{
		Tools:     reg,
		System:    VerifierPrompt,
		MaxTurns:  12,
		MaxTokens: orDefault(maxTokens, 8192),
		Model:     model,
	}
}

// runVerifierSession 跑一轮完整 verifier 会话（测试与引擎共用）。
func runVerifierSession(prov llm.Provider, bashExec tools.BashExec, findingSummary string) string {
	opts := AssembleVerifier(bashExec, "verifier-model", 8192)
	opts.Provider = prov
	sess := harness.NewSession(opts, "verifier-test")
	res, err := sess.Query(context.Background(), opts.System, "待验证发现："+findingSummary)
	if err != nil {
		return "verifier 会话失败: " + err.Error()
	}
	return res.FinalText
}

// RunVerifier 是导出版（引擎接线用）。
func RunVerifier(ctx context.Context, prov llm.Provider, bashExec tools.BashExec, findingSummary string) string {
	return runVerifierSession(prov, bashExec, findingSummary)
}
