package benchmark

import (
	"os/exec"
	"strings"
)

// fakeExec 记录命令调用（测试用）。
type fakeExec struct {
	calls      []string
	portOutput string
}

func (f *fakeExec) Run(cmd *exec.Cmd) (string, error) {
	f.calls = append(f.calls, strings.Join(cmd.Args, " "))
	if strings.Contains(strings.Join(cmd.Args, " "), "compose ps") {
		return f.portOutput, nil
	}
	return "ok", nil
}

func (f *fakeExec) called(kind string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, "compose "+kind) {
			return true
		}
	}
	return false
}
