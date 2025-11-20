package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBodyIncludes(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	dataDir := filepath.Join(configDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("创建 data 目录失败: %v", err)
	}

	expected := `{"hello":"world"}`
	payloadPath := filepath.Join(dataDir, "payload.json")
	if err := os.WriteFile(payloadPath, []byte(expected), 0o644); err != nil {
		t.Fatalf("写入 payload 失败: %v", err)
	}

	cfg := AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider: "demo",
				Service:  "codex",
				Body:     "!include data/payload.json",
			},
		},
	}

	if err := cfg.ResolveBodyIncludes(configDir); err != nil {
		t.Fatalf("解析 include 失败: %v", err)
	}

	if cfg.Monitors[0].Body != expected {
		t.Fatalf("body 解析结果不符合预期，got=%s", cfg.Monitors[0].Body)
	}
}

func TestResolveBodyIncludesRejectsOutsideData(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	cfg := AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider: "demo",
				Service:  "codex",
				Body:     "!include ../secret.json",
			},
		},
	}

	if err := cfg.ResolveBodyIncludes(configDir); err == nil {
		t.Fatalf("期望 include 非 data 目录时报错")
	}
}
