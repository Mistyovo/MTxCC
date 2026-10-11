package tools

import "testing"

// TestCatastrophicBash 验证灾难命令正则：四类无条件拒绝（含空格变形），变量形式不误伤。
// 令其失败的生产改动：正则缺失一类、或过宽误伤正常命令。
func TestCatastrophicBash(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"rm -rf /", true},
		{"rm -rf  /", true},                       // 双空格变形
		{"rm -fr /", true},                        // flag 顺序变形
		{"sudo rm -rf /", true},                   // 前缀
		{"rm -rf /etc", true},                     // 根下路径
		{"rm -rf ./build", false},                 // 相对路径正常清理
		{"rm -rf /tmp/mtx-task-9", false},         // 任务目录正常清理
		{":(){ :|:& };:", true},                   // fork 炸弹
		{":(){ :|:& };&", true},                   // 变形
		{"mkfs /dev/sda1", true},                  // 格式化
		{"mkfs.ext4 /dev/sdb", true},              // 带后缀
		{"dd if=/dev/zero of=/dev/sda", true},     // dd 写盘
		{"dd if=x.img of=/dev/nvme0n1", true},     // nvme
		{"dd if=a of=b.txt", false},               // 正常 dd 文件
		{"ls -la /", false},                       // 无害
		{"echo ${VAR}", false},                    // 变量引用无害
		{"nmap -sV $TARGET", false},               // 变量目标不在此层拦（RoE 层处理）
		{"cat /etc/shadow", false},                // 敏感读取非灾难（审批层处理）
	}
	for _, c := range cases {
		if got := CatastrophicBash(c.cmd); got != c.want {
			t.Errorf("CatastrophicBash(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}
