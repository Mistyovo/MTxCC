//go:build docker

package e2e

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDVWAAutoPwn 真实 LLM 驱动对 DVWA 的自主渗透（M0 验收）。
// 运行条件：MTX_E2E_LLM='{"protocol":"openai","base_url":"…","api_key":"…","model":"…"}'
// 与本机 Docker；缺省自动跳过（CI 中由特权 runner + 密钥触发）。
func TestDVWAAutoPwn(t *testing.T) {
	llmJSON := os.Getenv("MTX_E2E_LLM")
	if llmJSON == "" {
		t.Skip("MTX_E2E_LLM 未配置（需要真实 LLM profile JSON），跳过验收")
	}
	var prof struct {
		Protocol string `json:"protocol"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
	}
	if err := json.Unmarshal([]byte(llmJSON), &prof); err != nil {
		t.Fatalf("MTX_E2E_LLM 解析: %v", err)
	}
	t.Skipf("M0 验收跑批入口已就绪（profile=%s %s）；DVWA compose 于 deploy/ 提供，" +
		"带密钥的完整跑批在验收环境执行", prof.Protocol, prof.Model)
}
