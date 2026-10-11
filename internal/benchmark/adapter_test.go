package benchmark

import (
	"testing"
)

// TestAdapterRegistry 注册与获取。
func TestAdapterRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register("dvwa", &DVWAAdapter{})
	reg.Register("xbow", &XBowAdapter{})

	if _, ok := reg.Get("dvwa"); !ok {
		t.Fatal("dvwa 未注册")
	}
	if _, ok := reg.Get("xbow"); !ok {
		t.Fatal("xbow 未注册")
	}
	if _, ok := reg.Get("nope"); ok {
		t.Fatal("不存在的不应返回")
	}
}

// TestXBowAuthRequired 无 token 时返回明确错误。
func TestXBowAuthRequired(t *testing.T) {
	x := &XBowAdapter{Token: ""}
	if _, err := x.ListChallenges(); err == nil {
		t.Fatal("无 token 应报错")
	}
}

// TestScoreReport 成绩报告结构。
func TestScoreReport(t *testing.T) {
	rep := ScoreReport{
		Adapter:  "dvwa",
		Model:    "test-model",
		Total:    10, Solved: 8,
		CostUSD: 3.50, AvgLatencySec: 120,
	}
	if rep.SuccessRate() != 80.0 {
		t.Fatalf("成功率 = %v", rep.SuccessRate())
	}
	comp := rep.CompareWithReference()
	if comp["decepticon"] == nil || comp["redamon"] == nil {
		t.Fatal("对比表缺参考系")
	}
}
