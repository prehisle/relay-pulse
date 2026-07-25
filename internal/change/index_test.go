package change

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"monitor/internal/apikey"
	"monitor/internal/config"
)

// testCipher 创建测试用的 KeyCipher（固定 hex key）。
func testCipher(t *testing.T) *apikey.KeyCipher {
	t.Helper()
	// 64 hex chars = 32 bytes
	c, err := apikey.NewKeyCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewKeyCipher: %v", err)
	}
	return c
}

func TestAuthIndex_Rebuild_Basic(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: "sk-test-key-001"},
		{Provider: "p2", Service: "cc", Channel: "ch2", APIKey: "sk-test-key-002"},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	// 能查到 p1
	candidates, _ := idx.Lookup("sk-test-key-001", cipher)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Provider != "p1" || candidates[0].Channel != "ch1" {
		t.Errorf("unexpected candidate: %+v", candidates[0])
	}

	// 能查到 p2
	candidates, _ = idx.Lookup("sk-test-key-002", cipher)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Provider != "p2" {
		t.Errorf("expected provider p2, got %s", candidates[0].Provider)
	}
}

func TestAuthIndex_Rebuild_SkipsDisabledParentNoKey(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "active", Service: "cc", Channel: "ch", APIKey: "sk-active-key-1"},
		{Provider: "disabled", Service: "cc", Channel: "ch", APIKey: "sk-disabled-key", Disabled: true},
		{Provider: "child", Service: "cc", Channel: "ch", APIKey: "sk-child-key-01", Parent: "p/s/c"},
		{Provider: "nokey", Service: "cc", Channel: "ch"},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	// 只有 active 应被索引
	if c, _ := idx.Lookup("sk-active-key-1", cipher); len(c) != 1 {
		t.Errorf("active: expected 1 candidate, got %d", len(c))
	}
	if c, _ := idx.Lookup("sk-disabled-key", cipher); len(c) != 0 {
		t.Errorf("disabled: expected 0 candidates, got %d", len(c))
	}
	if c, _ := idx.Lookup("sk-child-key-01", cipher); len(c) != 0 {
		t.Errorf("child: expected 0 candidates, got %d", len(c))
	}
}

func TestAuthIndex_Rebuild_NameFallback(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "provID", Service: "cc", Channel: "chID", APIKey: "sk-fallback-key1"},
		{Provider: "provID2", Service: "cc", Channel: "chID2", APIKey: "sk-named-key-01",
			ProviderName: "Custom Name", ChannelName: "Custom Ch"},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	// 无 ProviderName 时回退到 Provider
	c1, _ := idx.Lookup("sk-fallback-key1", cipher)
	if len(c1) != 1 {
		t.Fatalf("expected 1 candidate")
	}
	if c1[0].ProviderName != "provID" {
		t.Errorf("ProviderName fallback: got %q, want %q", c1[0].ProviderName, "provID")
	}
	if c1[0].ChannelName != "chID" {
		t.Errorf("ChannelName fallback: got %q, want %q", c1[0].ChannelName, "chID")
	}

	// 有 ProviderName 时使用自定义名
	c2, _ := idx.Lookup("sk-named-key-01", cipher)
	if len(c2) != 1 {
		t.Fatalf("expected 1 candidate")
	}
	if c2[0].ProviderName != "Custom Name" {
		t.Errorf("ProviderName: got %q, want %q", c2[0].ProviderName, "Custom Name")
	}
	if c2[0].ChannelName != "Custom Ch" {
		t.Errorf("ChannelName: got %q, want %q", c2[0].ChannelName, "Custom Ch")
	}
}

func TestAuthIndex_Rebuild_SameKeyMultipleCandidates(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	// 同一 API Key 用于两个不同通道
	key := "sk-shared-key-01"
	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: key},
		{Provider: "p1", Service: "cc", Channel: "ch2", APIKey: key},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	candidates, _ := idx.Lookup(key, cipher)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
}

func TestAuthIndex_Lookup_NotFound(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: "sk-exists-key-1"},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	// 不存在的 key
	if c, _ := idx.Lookup("sk-nonexistent1", cipher); len(c) != 0 {
		t.Errorf("expected empty slice for non-existent key, got %v", c)
	}
}

func TestAuthIndex_Lookup_DeepCopy(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: "sk-deepcopy-key"},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	c1, _ := idx.Lookup("sk-deepcopy-key", cipher)
	c1[0].Provider = "mutated"

	// 再次查询应不受影响
	c2, _ := idx.Lookup("sk-deepcopy-key", cipher)
	if c2[0].Provider != "p1" {
		t.Errorf("internal state was mutated: got %q, want %q", c2[0].Provider, "p1")
	}
}

func TestAuthIndex_Rebuild_ReplacesOldIndex(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors1 := []config.ServiceConfig{
		{Provider: "old", Service: "cc", Channel: "ch", APIKey: "sk-old-key-1234"},
	}
	idx.Rebuild(monitors1, cipher, nil, nil)

	if c, _ := idx.Lookup("sk-old-key-1234", cipher); len(c) != 1 {
		t.Fatalf("expected 1 candidate before rebuild")
	}

	// 重建后旧 key 应消失
	monitors2 := []config.ServiceConfig{
		{Provider: "new", Service: "cc", Channel: "ch", APIKey: "sk-new-key-1234"},
	}
	idx.Rebuild(monitors2, cipher, nil, nil)

	if c, _ := idx.Lookup("sk-old-key-1234", cipher); len(c) != 0 {
		t.Errorf("old key should be gone after rebuild, got %d candidates", len(c))
	}
	if c, _ := idx.Lookup("sk-new-key-1234", cipher); len(c) != 1 {
		t.Errorf("new key should exist, got %d candidates", len(c))
	}
}

func TestAuthIndex_Lookup_MonitorKey(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "svc", Channel: "ch", APIKey: "sk-monkey-key-1"},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	c, _ := idx.Lookup("sk-monkey-key-1", cipher)
	if len(c) != 1 {
		t.Fatalf("expected 1 candidate")
	}
	if c[0].MonitorKey != "p1--svc--ch" {
		t.Errorf("MonitorKey: got %q, want %q", c[0].MonitorKey, "p1--svc--ch")
	}
}

func TestAuthIndex_Lookup_ApplyMode_ManualWithoutMonitorStore(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch", APIKey: "sk-manual-key-1"},
	}
	// nil monitorStore → always manual
	idx.Rebuild(monitors, cipher, nil, nil)

	c, _ := idx.Lookup("sk-manual-key-1", cipher)
	if len(c) != 1 {
		t.Fatalf("expected 1 candidate")
	}
	if c[0].ApplyMode != "manual" {
		t.Errorf("ApplyMode: got %q, want %q", c[0].ApplyMode, "manual")
	}
}

func TestAuthIndex_Lookup_KeyLast4(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch", APIKey: "sk-test-abcd"},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	c, _ := idx.Lookup("sk-test-abcd", cipher)
	if len(c) != 1 {
		t.Fatalf("expected 1 candidate")
	}
	if c[0].KeyLast4 != "abcd" {
		t.Errorf("KeyLast4: got %q, want %q", c[0].KeyLast4, "abcd")
	}
}

func TestAuthIndex_ConcurrentLookup(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch", APIKey: "sk-concurrent-k1"},
	}
	idx.Rebuild(monitors, cipher, nil, nil)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := idx.Lookup("sk-concurrent-k1", cipher)
			if len(c) != 1 {
				t.Errorf("concurrent lookup: expected 1 candidate")
			}
		}()
	}
	wg.Wait()
}

func TestAuthIndex_Rebuild_ApplyModeWithMonitorStore(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	// 创建临时 monitors.d/ 目录，写入一个 YAML 文件
	tmpDir := t.TempDir()
	yamlContent := `metadata:
  source: manual
  revision: 1
  created_at: "2026-01-01T00:00:00Z"
  updated_at: "2026-01-01T00:00:00Z"
monitors:
  - provider: p1
    service: cc
    channel: ch1
    api_key: "sk-test-key-001"
`
	if err := os.WriteFile(filepath.Join(tmpDir, "p1--cc--ch1.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ms := config.NewMonitorStore(tmpDir)

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: "sk-auto-mode-k1"},
		{Provider: "p2", Service: "cc", Channel: "ch2", APIKey: "sk-manual-mode1"},
	}
	idx.Rebuild(monitors, cipher, ms, nil)

	// p1--cc--ch1 存在于 monitors.d/ → auto
	c1, _ := idx.Lookup("sk-auto-mode-k1", cipher)
	if len(c1) != 1 {
		t.Fatalf("expected 1 candidate for p1, got %d", len(c1))
	}
	if c1[0].ApplyMode != "auto" {
		t.Errorf("p1 ApplyMode: got %q, want %q", c1[0].ApplyMode, "auto")
	}

	// p2--cc--ch2 不在 monitors.d/ → manual
	c2, _ := idx.Lookup("sk-manual-mode1", cipher)
	if len(c2) != 1 {
		t.Fatalf("expected 1 candidate for p2, got %d", len(c2))
	}
	if c2[0].ApplyMode != "manual" {
		t.Errorf("p2 ApplyMode: got %q, want %q", c2[0].ApplyMode, "manual")
	}
}
