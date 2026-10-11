// Package config 加载 MTxCC 运行配置（环境变量 + 默认值）。
package config

import (
	"fmt"
	"os"
)

// Config 是进程级配置。字段含义见 spec §2.2 与 deploy 说明。
type Config struct {
	ListenAddr  string // HTTP 监听地址，默认 :8080
	DatabaseURL string // PostgreSQL DSN
	DataDir     string // 数据目录（transcript/流量侧车/CA/任务卷根）
	MasterKey   string // 32 字节 base64，用于 AES-GCM 加密 api_key 等敏感列
}

// Load 从环境变量读取配置并校验必填项。
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:  envOr("MTX_LISTEN_ADDR", ":8080"),
		DatabaseURL: os.Getenv("MTX_DATABASE_URL"),
		DataDir:     envOr("MTX_DATA_DIR", "./data"),
		MasterKey:   os.Getenv("MTX_MASTER_KEY"),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("MTX_DATABASE_URL 未设置")
	}
	if cfg.MasterKey == "" {
		return nil, fmt.Errorf("MTX_MASTER_KEY 未设置（安装脚本应生成 32 字节 base64）")
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
