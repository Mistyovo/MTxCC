package sandbox

import (
	"os"
	"path/filepath"
)

// tempEgressDir 建网关规则挂载目录（挂到容器需同 rootless 可见性，用系统 tmp）。
func tempEgressDir() (string, error) {
	return os.MkdirTemp("/tmp", "mtx-egress-")
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

var _ = filepath.Join
