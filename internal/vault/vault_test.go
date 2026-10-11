package vault

import (
	"strings"
	"testing"
)

func testVault(t *testing.T) *Vault {
	t.Helper()
	v, err := New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestRoundtripAndPlaceholder 存取往返 + 占位符语法。
func TestRoundtripAndPlaceholder(t *testing.T) {
	v := testVault(t)
	if err := v.Put(7, "password", "root", "S3cret!Pass"); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get(7, "root")
	if err != nil || got != "S3cret!Pass" {
		t.Fatalf("Get = %q %v", got, err)
	}
	if ph := Placeholder("root"); ph != "${CRED:root}" {
		t.Fatalf("占位符 = %q", ph)
	}

	// 密文不可逆推（加密存储）：底层导出检查。
	if enc, _ := v.DumpEncrypted(7, "root"); strings.Contains(enc, "S3cret!Pass") {
		t.Fatal("密文含明文")
	}
}

// TestMaskingRoundtrip 发送前还原 / 输出回灌前再掩码（Review Focus 3）。
func TestMaskingRoundtrip(t *testing.T) {
	v := testVault(t)
	_ = v.Put(7, "password", "root", "S3cret!Pass")

	// 模型给的命令带占位符 → 发送前还原。
	cmd := v.Expand(7, "curl -u admin:${CRED:root} http://10.0.0.5/")
	if !strings.Contains(cmd, "S3cret!Pass") {
		t.Fatalf("未还原: %q", cmd)
	}
	// 执行输出带回真值 → 回灌前掩码。
	out := v.Mask(7, "login ok for admin S3cret!Pass welcome")
	if strings.Contains(out, "S3cret!Pass") {
		t.Fatalf("真值泄漏: %q", out)
	}
	if !strings.Contains(out, "${CRED:root}") {
		t.Fatalf("应替换为占位符: %q", out)
	}
}

// TestPlaceholderNeverLeaksInContext transcript 视角：占位符命令经 Expand→Mask 双向后
// 落盘文本不含真值（Review Focus 3 的集成断言）。
func TestPlaceholderNeverLeaksInContext(t *testing.T) {
	v := testVault(t)
	_ = v.Put(9, "token", "api", "tk-1234567890abcdef")

	modelCmd := "curl -H 'X-API: ${CRED:api}' http://10.0.0.9/api"
	sent := v.Expand(9, modelCmd)          // 发送瞬间的真值形态
	result := "200 OK token tk-1234567890abcdef valid"
	masked := v.Mask(9, result) // 回灌模型前

	for _, s := range []string{modelCmd, masked} {
		if strings.Contains(s, "tk-1234567890abcdef") {
			t.Fatalf("真值泄漏到模型可见文本: %q", s)
		}
	}
	if !strings.Contains(sent, "tk-1234567890abcdef") {
		t.Fatal("Expand 未还原（发送应带真值）")
	}
}
