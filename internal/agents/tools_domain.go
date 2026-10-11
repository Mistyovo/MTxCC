// Package agents 装配各角色（planner/worker/goals 等）的 prompt 与工具集。
package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mtx/internal/graph"
	"mtx/internal/tools"
)

// ExecContext 携带当前执行的任务/探索/意图上下文（工具内自动注入，模型不可指定）。
type ExecContext struct {
	TaskID   int64
	ExpID    int64
	IntentID int64
	Role     string
}

// DomainTools 生成 worker 写回六件套 + 查询工具（spec §6.2）。
func DomainTools(g *graph.GraphStore, ec ExecContext) []tools.Tool {
	reg := []tools.Tool{
		&addIntentTool{g, ec},
		&proveGoalTool{g, ec},
		&recordFactTool{g, ec},
		&insertAssetsTool{g, ec},
		&reportFindingTool{g, ec},
		&graphOverviewTool{g, ec},
		&listFindingsTool{g, ec},
		&nodeDetailTool{g, ec},
		&getWorkerTraceTool{g, ec},
		&searchTracesTool{g, ec},
	}
	return reg
}

// --- add_intent ---

type addIntentTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *addIntentTool) Name() string { return "add_intent" }
func (t *addIntentTool) Description() string {
	return `派发探索意图（每次最多 4 条，串行利用链一次只派"前置已满足"的下一步）。
parent_ids 只准填 fact/finding 节点 ID（意图必须锚在已确认知识上）。
intents 数组元素：{title, priority(1-10), parent_ids?, anchor_assets?}。`
}
func (t *addIntentTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"intents": map[string]any{"type": "array", "description": "1-4 条意图"},
	}, "required": []string{"intents"}}
}
func (t *addIntentTool) Flags() tools.Flags { return tools.Flags{} }
func (t *addIntentTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *addIntentTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Intents []struct {
			Title       string  `json:"title"`
			Priority    int     `json:"priority"`
			ParentIDs   []int64 `json:"parent_ids"`
			AnchorAssets []int64 `json:"anchor_assets"`
		} `json:"intents"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if len(in.Intents) == 0 {
		return "", fmt.Errorf("intents 不能为空")
	}
	if len(in.Intents) > 4 {
		return "", fmt.Errorf("一次最多 4 条意图（收到 %d）：把剩余方向留到下次唤醒", len(in.Intents))
	}
	var ids []int64
	for _, it := range in.Intents {
		if strings.TrimSpace(it.Title) == "" {
			return "", fmt.Errorf("意图 title 不能为空")
		}
		if it.Priority <= 0 {
			it.Priority = 5
		}
		got, err := t.g.AddIntent(ctx, t.ec.ExpID, it.ParentIDs, it.Title, it.Priority, it.AnchorAssets)
		if err != nil {
			return "", err
		}
		ids = append(ids, got...)
	}
	out, _ := json.Marshal(map[string]any{"intent_ids": ids})
	return string(out), nil
}

// --- prove_goal ---

type proveGoalTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *proveGoalTool) Name() string { return "prove_goal" }
func (t *proveGoalTool) Description() string {
	return "标记目标达成状态：{goal_id, state: met|abandoned, reason}。全部目标 met 后任务自动完成。"
}
func (t *proveGoalTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"goal_id": map[string]any{"type": "integer"},
		"state":   map[string]any{"type": "string", "enum": []string{"met", "abandoned"}},
		"reason":  map[string]any{"type": "string"},
	}, "required": []string{"goal_id", "state"}}
}
func (t *proveGoalTool) Flags() tools.Flags { return tools.Flags{} }
func (t *proveGoalTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *proveGoalTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		GoalID int64  `json:"goal_id"`
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if in.GoalID <= 0 {
		return "", fmt.Errorf("goal_id 必填")
	}
	if in.State != "met" && in.State != "abandoned" {
		return "", fmt.Errorf("state 只能是 met|abandoned")
	}
	tag, err := t.g.DB().Exec(ctx, `UPDATE exploration_nodes
		SET state=$1, updated_at=now() WHERE id=$2 AND kind='goal'`, in.State, in.GoalID)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", fmt.Errorf("goal %d 不存在", in.GoalID)
	}
	return fmt.Sprintf(`{"goal_id":%d,"state":%q}`, in.GoalID, in.State), nil
}

// --- record_fact ---

type recordFactTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *recordFactTool) Name() string { return "record_fact" }
func (t *recordFactTool) Description() string {
	return `把探索结论写回知识图谱：{title, body, confidence: observed|inferred, anchor_assets?}。
多个观察汇总成一条增量事实；只写真实看到的。`
}
func (t *recordFactTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"title":       map[string]any{"type": "string"},
		"body":        map[string]any{"type": "string"},
		"confidence":  map[string]any{"type": "string", "enum": []string{"observed", "inferred"}},
		"anchor_assets": map[string]any{"type": "array"},
	}, "required": []string{"title", "body"}}
}
func (t *recordFactTool) Flags() tools.Flags { return tools.Flags{} }
func (t *recordFactTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *recordFactTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Title       string  `json:"title"`
		Body        string  `json:"body"`
		Confidence  string  `json:"confidence"`
		AnchorAssets []int64 `json:"anchor_assets"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" {
		return "", fmt.Errorf("title 必填")
	}
	if in.Confidence == "" {
		in.Confidence = "inferred"
	}
	id, err := t.g.RecordFact(ctx, t.ec.ExpID, t.ec.IntentID, in.Title, json.RawMessage(`"`+jsonEscape(in.Body)+`"`), in.Confidence, in.AnchorAssets)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"node_id": id})
	return string(out), nil
}

// --- insert_assets ---

type insertAssetsTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *insertAssetsTool) Name() string { return "insert_assets" }
func (t *insertAssetsTool) Description() string {
	return `登记新资产（程序自动去重）：assets 数组元素 {type: domain|service|endpoint|ip, key, attrs?}。
key 规则：domain=域名、service=host:port:scheme、endpoint=url+method。`
}
func (t *insertAssetsTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"assets": map[string]any{"type": "array"},
	}, "required": []string{"assets"}}
}
func (t *insertAssetsTool) Flags() tools.Flags { return tools.Flags{} }
func (t *insertAssetsTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *insertAssetsTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Assets []struct {
			Type  string         `json:"type"`
			Key   string         `json:"key"`
			Attrs map[string]any `json:"attrs"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if len(in.Assets) == 0 {
		return "", fmt.Errorf("assets 不能为空")
	}
	raw := make([]graph.RawAsset, 0, len(in.Assets))
	for _, a := range in.Assets {
		if a.Key == "" {
			return "", fmt.Errorf("资产 key 必填（type=%s）", a.Type)
		}
		raw = append(raw, graph.RawAsset{Type: a.Type, Key: a.Key, Attrs: a.Attrs})
	}
	ids, err := t.g.InsertAssets(ctx, 0, raw, t.ec.TaskID)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"asset_ids": ids})
	return string(out), nil
}

// --- report_finding ---

type reportFindingTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *reportFindingTool) Name() string { return "report_finding" }
func (t *reportFindingTool) Description() string {
	return `上报确认漏洞。只有本次真实触发过、拿到可复现证据才用——
严禁把"版本匹配 CVE""参数看起来可注入"当已确认。
severity 不得高于证据等级：declared≤low、inferred≤medium、runtime_observed≤critical。`
}
func (t *reportFindingTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"vulnclass":  map[string]any{"type": "string"},
		"severity":   map[string]any{"type": "string", "enum": []string{"info", "low", "medium", "high", "critical"}},
		"confidence": map[string]any{"type": "string", "enum": []string{"declared", "inferred", "runtime_observed"}},
		"title":      map[string]any{"type": "string"},
		"summary":    map[string]any{"type": "string"},
		"evidence":   map[string]any{"type": "array", "description": "[{kind:tool_output|traffic|file, ref, excerpt}]"},
	}, "required": []string{"title", "severity", "confidence", "vulnclass"}}
}
func (t *reportFindingTool) Flags() tools.Flags { return tools.Flags{} }
func (t *reportFindingTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *reportFindingTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Vulnclass  string          `json:"vulnclass"`
		Severity   string          `json:"severity"`
		Confidence string          `json:"confidence"`
		Title      string          `json:"title"`
		Summary    string          `json:"summary"`
		Evidence   json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" {
		return "", fmt.Errorf("title 必填")
	}
	if in.Severity == "" || in.Confidence == "" {
		return "", fmt.Errorf("severity 与 confidence 必填（证据门需要）")
	}
	id, err := t.g.ReportFinding(ctx, t.ec.TaskID, t.ec.ExpID, t.ec.IntentID, graph.FindingIn{
		Vulnclass: in.Vulnclass, Severity: in.Severity, Confidence: in.Confidence,
		Title: in.Title, Summary: in.Summary, Evidence: in.Evidence,
	})
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"finding_id": id, "status": "candidate"})
	return string(out), nil
}

// --- graph_overview / list_findings / node_detail / get_worker_trace / search_traces ---

type graphOverviewTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *graphOverviewTool) CheckPerms(context.Context, json.RawMessage) error { return nil }
func (t *graphOverviewTool) Flags() tools.Flags { return tools.Flags{ReadOnly: true, Concurrent: true} }

func (t *graphOverviewTool) Name() string { return "graph_overview" }
func (t *graphOverviewTool) Description() string {
	return "读当前探索态势：goals/intents 状态计数、最近事实、死亡向量（已试死的方向）。"
}
func (t *graphOverviewTool) Schema() map[string]any { return map[string]any{"type": "object"} }

func (t *graphOverviewTool) Call(ctx context.Context, _ json.RawMessage) (string, error) {
	ov, err := t.g.GraphOverview(ctx, t.ec.ExpID)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"goals": ov.Goals, "intents": ov.Intents,
		"recent_facts": ov.RecentFacts, "dead_vectors": ov.DeadVectors,
	})
	return string(out), nil
}

type listFindingsTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *listFindingsTool) CheckPerms(context.Context, json.RawMessage) error { return nil }
func (t *listFindingsTool) Flags() tools.Flags { return tools.Flags{ReadOnly: true, Concurrent: true} }

func (t *listFindingsTool) Name() string { return "list_findings" }
func (t *listFindingsTool) Description() string { return "列出当前任务的发现（含验证状态）。" }
func (t *listFindingsTool) Schema() map[string]any { return map[string]any{"type": "object"} }

func (t *listFindingsTool) Call(ctx context.Context, _ json.RawMessage) (string, error) {
	rows, err := t.g.DB().Query(ctx, `SELECT id, severity, status, verify_state, title
		FROM findings WHERE task_id=$1 ORDER BY id DESC LIMIT 50`, t.ec.TaskID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var sev, st, vs, title string
		_ = rows.Scan(&id, &sev, &st, &vs, &title)
		out = append(out, map[string]any{"id": id, "severity": sev, "status": st, "verify_state": vs, "title": title})
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

type nodeDetailTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *nodeDetailTool) CheckPerms(context.Context, json.RawMessage) error { return nil }
func (t *nodeDetailTool) Flags() tools.Flags { return tools.Flags{ReadOnly: true, Concurrent: true} }

func (t *nodeDetailTool) Name() string { return "node_detail" }
func (t *nodeDetailTool) Description() string { return "按节点 ID 读详情：{node_id}。" }
func (t *nodeDetailTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"node_id": map[string]any{"type": "integer"},
	}, "required": []string{"node_id"}}
}

func (t *nodeDetailTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct{ NodeID int64 `json:"node_id"` }
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	var kind, state, title string
	var body []byte
	err := t.g.DB().QueryRow(ctx, `SELECT kind, state, title, body::text
		FROM exploration_nodes WHERE id=$1`, in.NodeID).Scan(&kind, &state, &title, &body)
	if err != nil {
		return "", fmt.Errorf("节点 %d 不存在", in.NodeID)
	}
	out, _ := json.Marshal(map[string]any{"id": in.NodeID, "kind": kind, "state": state, "title": title, "body": json.RawMessage(body)})
	return string(out), nil
}

type getWorkerTraceTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *getWorkerTraceTool) CheckPerms(context.Context, json.RawMessage) error { return nil }
func (t *getWorkerTraceTool) Flags() tools.Flags { return tools.Flags{ReadOnly: true, Concurrent: true} }

func (t *getWorkerTraceTool) Name() string { return "get_worker_trace" }
func (t *getWorkerTraceTool) Description() string { return "读某意图的执行过程：{intent_id}。" }
func (t *getWorkerTraceTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"intent_id": map[string]any{"type": "integer"},
	}, "required": []string{"intent_id"}}
}

func (t *getWorkerTraceTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct{ IntentID int64 `json:"intent_id"` }
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	hits, err := t.g.SearchTraces(ctx, t.ec.TaskID, "", in.IntentID, 100)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(hits)
	return string(b), nil
}

type searchTracesTool struct{ g *graph.GraphStore; ec ExecContext }

func (t *searchTracesTool) CheckPerms(context.Context, json.RawMessage) error { return nil }
func (t *searchTracesTool) Flags() tools.Flags { return tools.Flags{ReadOnly: true, Concurrent: true} }

func (t *searchTracesTool) Name() string { return "search_traces" }
func (t *searchTracesTool) Description() string {
	return "跨意图全文检索执行过程：{query}。发请求前先查是否已有人测过同样的东西。"
}
func (t *searchTracesTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"query": map[string]any{"type": "string"},
	}, "required": []string{"query"}}
}

func (t *searchTracesTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct{ Query string `json:"query"` }
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	hits, err := t.g.SearchTraces(ctx, t.ec.TaskID, in.Query, t.ec.IntentID, 20)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(hits)
	return string(b), nil
}

// jsonEscape 转义为 JSON 字符串字面量内容。
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
