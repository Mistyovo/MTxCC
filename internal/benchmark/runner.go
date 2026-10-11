package benchmark

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

// Runner 评测执行器（M3：对接 server API 逐题跑）。
type Runner struct {
	Adapter Adapter
	APIBase string // MTxCC API 地址
	Token   string // MTxCC JWT
}

// RunSuite 跑整套基准。
func (r *Runner) RunSuite() (*ScoreReport, error) {
	challenges, err := r.Adapter.ListChallenges()
	if err != nil {
		return nil, err
	}
	rep := &ScoreReport{Adapter: r.Adapter.Name(), Total: len(challenges)}
	for _, ch := range challenges {
		entryURL, err := r.Adapter.StartChallenge(ch.ID)
		if err != nil {
			continue
		}
		// TODO M3 跑分时接线：POST /api/tasks {target_text: entryURL} → 等终态 → 收 findings。
		_ = entryURL
		_ = r.Adapter.StopChallenge(ch.ID)
	}
	return rep, nil
}
