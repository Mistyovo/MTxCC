package benchmark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ScoreReport 是一次跑分的结构化成绩。
type ScoreReport struct {
	Adapter       string  `json:"adapter"`
	Model         string  `json:"model"`
	Total         int     `json:"total"`
	Solved        int     `json:"solved"`
	CostUSD       float64 `json:"cost_usd"`
	AvgLatencySec int     `json:"avg_latency_sec"`
	TokensUsed    int64   `json:"tokens_used"`
	FalsePositive int     `json:"false_positive"` // verifier 反证后否定数
}

// SuccessRate 成功率。
func (r ScoreReport) SuccessRate() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Solved) / float64(r.Total) * 100
}

// reference 是公开基准成绩参照（analysis 报告）。
var reference = map[string]struct {
	Total, Solved int
	Label         string
}{
	"decepticon": {104, 102, "Decepticon（XBOW 102/104）"},
	"redamon":    {104, 101, "RedAmon（XBOW 101/104）"},
	"cai":        {104, 100, "CAI（HTB AI vs Humans 第一）"},
}

// CompareWithReference 返回与已知最强系统的对比。
func (r ScoreReport) CompareWithReference() map[string]map[string]any {
	out := map[string]map[string]any{}
	for key, ref := range reference {
		out[key] = map[string]any{
			"label":  ref.Label,
			"total":  ref.Total,
			"solved": ref.Solved,
			"rate":   float64(ref.Solved) / float64(ref.Total) * 100,
			"ours":   r.SuccessRate(),
			"diff":   r.SuccessRate() - float64(ref.Solved)/float64(ref.Total)*100,
		}
	}
	return out
}

// ChallengeResult 单题结果。
type ChallengeResult struct {
	ID      string
	Title   string
	Solved  bool
	Findings int
	Err     string
}

// Runner 评测执行器：对接 MTxCC API 逐题跑真实任务。
type Runner struct {
	Adapter   Adapter
	APIBase   string // 如 http://127.0.0.1:8080
	Token     string // MTxCC JWT
	Model     string
	TimeoutSec int   // 单题超时（默认 600）
	PollMS    int   // 状态轮询间隔（默认 2000）
	Results   []ChallengeResult // 每题明细
}

// RunSuite 跑整套基准：建任务→等终态→收集 findings→评分。
func (r *Runner) RunSuite() (*ScoreReport, error) {
	if r.TimeoutSec <= 0 {
		r.TimeoutSec = 600
	}
	if r.PollMS <= 0 {
		r.PollMS = 2000
	}
	challenges, err := r.Adapter.ListChallenges()
	if err != nil {
		return nil, err
	}
	rep := &ScoreReport{Adapter: r.Adapter.Name(), Model: r.Model, Total: len(challenges)}
	latencies := []int{}

	for _, ch := range challenges {
		entryURL, err := r.Adapter.StartChallenge(ch.ID)
		if err != nil {
			r.Results = append(r.Results, ChallengeResult{ID: ch.ID, Title: ch.Title, Err: err.Error()})
			continue
		}
		start := time.Now()
		taskID, err := r.createTask(entryURL)
		if err != nil {
			r.Results = append(r.Results, ChallengeResult{ID: ch.ID, Title: ch.Title, Err: "create: " + err.Error()})
			_ = r.Adapter.StopChallenge(ch.ID)
			continue
		}
		status, err := r.waitTask(taskID)
		findings := 0
		if err == nil {
			findings, _ = r.countFindings(taskID)
		}
		elapsed := int(time.Since(start).Seconds())
		solved := status == "done" && findings > 0
		if solved {
			rep.Solved++
		}
		latencies = append(latencies, elapsed)
		r.Results = append(r.Results, ChallengeResult{
			ID: ch.ID, Title: ch.Title, Solved: solved, Findings: findings,
		})
		_ = r.Adapter.StopChallenge(ch.ID)
	}

	if n := len(latencies); n > 0 {
		sum := 0
		for _, l := range latencies {
			sum += l
		}
		rep.AvgLatencySec = sum / n
	}
	return rep, nil
}

func (r *Runner) createTask(targetURL string) (int64, error) {
	body, _ := json.Marshal(map[string]any{
		"target_text":     targetURL,
		"timeout_seconds": r.TimeoutSec,
		"roe": map[string]any{
			"mode":     "enforce",
			"in_scope": []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		},
	})
	resp, err := r.api("POST", "/api/tasks", body)
	if err != nil {
		return 0, err
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(resp, &out)
	if out.ID == 0 {
		return 0, fmt.Errorf("任务未创建: %s", string(resp))
	}
	return out.ID, nil
}

func (r *Runner) waitTask(taskID int64) (string, error) {
	deadline := time.Now().Add(time.Duration(r.TimeoutSec+120) * time.Second)
	for time.Now().Before(deadline) {
		resp, err := r.api("GET", "/api/tasks", nil)
		if err != nil {
			return "", err
		}
		var tasks []struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(resp, &tasks)
		for _, t := range tasks {
			if t.ID == taskID && (t.Status == "done" || t.Status == "failed" || t.Status == "timeout") {
				return t.Status, nil
			}
		}
		time.Sleep(time.Duration(r.PollMS) * time.Millisecond)
	}
	return "timeout", fmt.Errorf("轮询超时")
}

func (r *Runner) countFindings(taskID int64) (int, error) {
	// findings 经活动流中 report_finding 计数；M3 简化为 activity kind=result 后 findings 表查询。
	resp, err := r.api("GET", fmt.Sprintf("/api/exploration/%d/activity?limit=500", taskID), nil)
	if err != nil {
		return 0, err
	}
	// report_finding 成功的 activity 载荷含 finding_id（载荷是转义 JSON 字符串，搜裸键名）。
	return strings.Count(string(resp), "finding_id"), nil
}

func (r *Runner) api(method, path string, body []byte) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, strings.TrimRight(r.APIBase, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return b, nil
}
