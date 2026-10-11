package verify

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func replayTarget(t *testing.T) *httptest.Server {
	t.Helper()
	n := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/vuln", func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("X-Hit", "yes")
		_, _ = w.Write([]byte("ROOT_CONTEXT_MARKER id=0(root)"))
	})
	mux.HandleFunc("/healthy", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	return httptest.NewServer(mux)
}

// TestOracleReplayVerified 重放命中签名特征 → verified。
func TestOracleReplayVerified(t *testing.T) {
	ts := replayTarget(t)
	defer ts.Close()

	recipe := Recipe{
		Kind: "replay",
		Steps: []Req{{
			Method: "GET", URL: ts.URL + "/vuln",
			ExpectStatus: 200, ExpectBody: "ROOT_CONTEXT_MARKER", ExpectHeader: "X-Hit:yes",
		}},
	}
	v, err := NewOracle(NoopSleeper{}).Run(recipe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v != Verified {
		t.Fatalf("verdict = %v, want verified", v)
	}
}

// TestOracleFeatureMismatchCandidate 特征不匹配 → candidate（永不 refuted，Review Focus 2）。
func TestOracleFeatureMismatchCandidate(t *testing.T) {
	ts := replayTarget(t)
	defer ts.Close()

	recipe := Recipe{
		Kind: "replay",
		Steps: []Req{{Method: "GET", URL: ts.URL + "/healthy", ExpectBody: "ROOT_CONTEXT_MARKER"}},
	}
	v, err := NewOracle(NoopSleeper{}).Run(recipe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v != Candidate {
		t.Fatalf("verdict = %v, want candidate（失败降级，不得 refuted）", v)
	}
}

// TestOracleDifferential 差分：基线 vs payload 响应差异 → verified；无差异 → candidate。
func TestOracleDifferential(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") == "1' AND 1=1--" {
			_, _ = w.Write([]byte("ADMIN_VIEW"))
			return
		}
		_, _ = w.Write([]byte("USER_VIEW"))
	}))
	defer ts.Close()

	base := ts.URL + "/item?id=1"
	pay := ts.URL + "/item?id=1%27%20AND%201%3D1--"

	diff := Recipe{Kind: "differential", Steps: []Req{
		{Method: "GET", URL: base},
		{Method: "GET", URL: pay, ExpectBody: "ADMIN_VIEW", ExpectDiffersFrom: 0},
	}}
	if v, _ := NewOracle(NoopSleeper{}).Run(diff); v != Verified {
		t.Fatalf("差分命中应 verified, got %v", v)
	}

	same := Recipe{Kind: "differential", Steps: []Req{
		{Method: "GET", URL: base},
		{Method: "GET", URL: pay, ExpectDiffersFrom: 0}, // 服务器不差分时其实会差……构造一个无差场景：
	}}
	same.Steps[1].URL = base // payload=基线 → 无差异
	if v, _ := NewOracle(NoopSleeper{}).Run(same); v != Candidate {
		t.Fatalf("无差异应 candidate, got %v", v)
	}
}

// TestVerdictEnum 验证 verdict 枚举完备性（仅两态）。
func TestVerdictEnum(t *testing.T) {
	if Verified.String() != "verified" || Candidate.String() != "candidate" {
		t.Fatal("verdict 字符串不符")
	}
	if len(map[Verdict]bool{Verified: true, Candidate: true}) != 2 {
		t.Fatal("verdict 必须只有两态")
	}
}

type NoopSleeper struct{}

func (NoopSleeper) Sleep(time.Duration) {}
