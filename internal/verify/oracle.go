// Package verify 实现机器 oracle（spec §8.1）：finding 的 verification_recipe
// 由确定性执行器复跑，verdict 只有 verified/candidate 两态——永不 refuted（证明或保留）。
package verify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Verdict 是 oracle 结论。
type Verdict int

const (
	// Candidate 未证实（保留，不武断否定）。
	Candidate Verdict = iota
	// Verified 特征复现确认。
	Verified
)

// String 见 fmt.Stringer。
func (v Verdict) String() string {
	if v == Verified {
		return "verified"
	}
	return "candidate"
}

// Req 是 recipe 的一步（重放请求 + 期望特征）。
type Req struct {
	Method           string
	URL              string
	Headers          map[string]string
	Body             string
	ExpectStatus     int
	ExpectBody       string
	ExpectHeader     string // "Key:value"
	ExpectDiffersFrom int   // 差分：与第 N 步的响应体必须不同
}

// Recipe 是 finding 的验证配方（重放/差分）。
type Recipe struct {
	Kind  string // replay | differential
	Steps []Req
}

// Sleeper 抽象等待（测试注入）。
type Sleeper interface{ Sleep(time.Duration) }

type realSleeper struct{}

// Sleep 见 Sleeper。
func (realSleeper) Sleep(d time.Duration) { time.Sleep(d) }

// RealSleeper 生产等待器。
func RealSleeper() Sleeper { return realSleeper{} }

// Oracle 确定性执行器（无 LLM）。
type Oracle struct {
	client  *http.Client
	sleeper Sleeper
}

// NewOracle 建 oracle（sleeper 可注入）。
func NewOracle(s Sleeper) *Oracle {
	return &Oracle{
		client:  &http.Client{Timeout: 20 * time.Second},
		sleeper: s,
	}
}

// response 是一步的抓取结果。
type response struct {
	status int
	header http.Header
	body   string
}

// Run 复跑 recipe：全部步骤特征命中 → verified；任一未命中/失败 → candidate。
// 步间 200ms 间隔（OPSEC 速率纪律）。
func (o *Oracle) Run(r Recipe) (Verdict, error) {
	if len(r.Steps) == 0 {
		return Candidate, fmt.Errorf("空 recipe")
	}
	resps := make([]response, len(r.Steps))
	for i, s := range r.Steps {
		if i > 0 {
			o.sleeper.Sleep(200 * time.Millisecond)
		}
		resp, err := o.fetch(s)
		if err != nil {
			return Candidate, nil // 网络失败≠否定：降级 candidate
		}
		resps[i] = resp
		if !matchFeatures(s, resp) {
			return Candidate, nil
		}
		if s.ExpectDiffersFrom >= 0 && s.ExpectDiffersFrom < i {
			if strings.TrimSpace(resps[s.ExpectDiffersFrom].body) == strings.TrimSpace(resp.body) {
				return Candidate, nil // 无差分
			}
		}
	}
	return Verified, nil
}

func (o *Oracle) fetch(s Req) (response, error) {
	method := s.Method
	if method == "" {
		method = http.MethodGet
	}
	var rd io.Reader
	if s.Body != "" {
		rd = strings.NewReader(s.Body)
	}
	req, err := http.NewRequest(method, s.URL, rd)
	if err != nil {
		return response{}, err
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return response{}, err
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(b)}, nil
}

// matchFeatures 校验一步的特征期望（未设的期望跳过）。
func matchFeatures(s Req, r response) bool {
	if s.ExpectStatus != 0 && r.status != s.ExpectStatus {
		return false
	}
	if s.ExpectBody != "" && !strings.Contains(r.body, s.ExpectBody) {
		return false
	}
	if s.ExpectHeader != "" {
		k, v, _ := strings.Cut(s.ExpectHeader, ":")
		if strings.TrimSpace(r.header.Get(strings.TrimSpace(k))) != strings.TrimSpace(v) {
			return false
		}
	}
	return true
}

// EvidenceReplay 是 finding evidence 里的可重放条目（M3 自动验证）。
type EvidenceReplay struct {
	Kind        string `json:"kind"` // replay
	URL         string `json:"url"`
	ExpectBody  string `json:"expect_body"`
	ExpectStatus int   `json:"expect_status"`
}

// RecipesFromEvidence 从 finding evidence JSON 提取重放配方。
// 只认 kind=replay 条目；无 → 空（调用方按 candidate 处理，不武断 refuted）。
func RecipesFromEvidence(evidenceJSON string) []Recipe {
	var entries []EvidenceReplay
	if err := json.Unmarshal([]byte(evidenceJSON), &entries); err != nil {
		return nil
	}
	var out []Recipe
	for _, e := range entries {
		if e.Kind != "replay" || e.URL == "" {
			continue
		}
		out = append(out, Recipe{Kind: "replay", Steps: []Req{{
			Method: http.MethodGet, URL: e.URL,
			ExpectBody: e.ExpectBody, ExpectStatus: e.ExpectStatus,
			ExpectDiffersFrom: -1,
		}}})
	}
	return out
}

// AutoVerify 对 finding evidence 全自动机器验证：
// 提取 recipe → oracle 复跑 → 全部 verified 才返回 Verified，否则 Candidate（永不 refuted）。
func AutoVerify(s Sleeper, evidenceJSON string) Verdict {
	recipes := RecipesFromEvidence(evidenceJSON)
	if len(recipes) == 0 {
		return Candidate
	}
	o := NewOracle(s)
	all := true
	for _, r := range recipes {
		v, err := o.Run(r)
		if err != nil || v != Verified {
			all = false
		}
	}
	if all {
		return Verified
	}
	return Candidate
}
