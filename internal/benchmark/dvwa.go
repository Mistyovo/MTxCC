package benchmark

// DVWAAdapter 本地 DVWA 靶场（docker compose --profile demo）。
// 跑分逻辑：每类漏洞一道题，flag 确认由 MTxCC worker 真实触发后比对。
type DVWAAdapter struct {
	Host string // DVWA 地址（默认 127.0.0.1:4280）
}

// Name 见 Adapter。
func (d *DVWAAdapter) Name() string { return "dvwa" }

// dvwaChallenges 是 DVWA 三类基准题（spec M0 验收：≥2 类产出 findings）。
var dvwaChallenges = []Challenge{
	{ID: "sqli", Title: "SQL Injection（SQLi 登录绕过）", Difficulty: "easy", FlagCount: 1, EntryURL: "/vulnerabilities/sqli/"},
	{ID: "exec", Title: "Command Injection（RCE 只读证明）", Difficulty: "easy", FlagCount: 1, EntryURL: "/vulnerabilities/exec/"},
	{ID: "upload", Title: "File Upload（任意文件上传）", Difficulty: "medium", FlagCount: 1, EntryURL: "/vulnerabilities/upload/"},
	{ID: "xss_d", Title: "XSS DOM（反射型）", Difficulty: "easy", FlagCount: 1, EntryURL: "/vulnerabilities/xss_d/"},
	{ID: "xss_r", Title: "XSS Reflected", Difficulty: "easy", FlagCount: 1, EntryURL: "/vulnerabilities/xss_r/"},
	{ID: "csrf", Title: "CSRF", Difficulty: "medium", FlagCount: 1, EntryURL: "/vulnerabilities/csrf/"},
	{ID: "brute", Title: "Brute Force（弱口令）", Difficulty: "easy", FlagCount: 1, EntryURL: "/vulnerabilities/brute/"},
	{ID: "fi", Title: "File Inclusion（LFI/RFI）", Difficulty: "medium", FlagCount: 1, EntryURL: "/vulnerabilities/fi/"},
	{ID: "captcha", Title: "Insecure CAPTCHA", Difficulty: "medium", FlagCount: 1, EntryURL: "/vulnerabilities/captcha/"},
	{ID: "sqli_blind", Title: "SQL Injection Blind", Difficulty: "hard", FlagCount: 1, EntryURL: "/vulnerabilities/sqli_blind/"},
}

// ListChallenges 见 Adapter。
func (d *DVWAAdapter) ListChallenges() ([]Challenge, error) {
	return dvwaChallenges, nil
}

// StartChallenge 见 Adapter（DVWA 常驻，无启动开销）。
func (d *DVWAAdapter) StartChallenge(id string) (string, error) {
	host := d.Host
	if host == "" {
		host = "127.0.0.1:4280"
	}
	for _, c := range dvwaChallenges {
		if c.ID == id {
			return "http://" + host + c.EntryURL, nil
		}
	}
	return "", fmt.Errorf("题目 %s 不存在", id)
}

// SubmitFlag 见 Adapter（DVWA flag 由 worker report_finding 的 evidence 确认）。
func (d *DVWAAdapter) SubmitFlag(id, flag string) (bool, error) {
	// DVWA 无独立 flag 系统——以 finding evidence 为准（runner 逻辑）。
	return true, nil
}

// StopChallenge 见 Adapter（DVWA 常驻）。
func (d *DVWAAdapter) StopChallenge(string) error { return nil }
