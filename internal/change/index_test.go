package change

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"monitor/internal/apikey"
	"monitor/internal/config"
	"monitor/internal/probe"
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

// TestAuthIndex_Rebuild_DefaultTestVariant 锁死变更流程测试步的默认模板：
// 优先跟随通道自己在跑的模板，只在拿不到时才回落注册表默认值。
//
// 这条不是纯 UI 偏好：默认值决定了老中转商改 base_url / 轮换 key 时探哪个模型。
// 曾经一律取注册表默认值，新增一个字典序更靠前的模板（cc-fable-ping-20260806）即把所有
// 老通道的默认探测目标换成它们并未上架的 fable-5，变更请求提交不了。
func TestDefaultTestVariant(t *testing.T) {
	tt := &probe.TestType{
		ID:             "cc",
		DefaultVariant: "cc-registry-default",
		Variants: []*probe.PayloadVariant{
			{ID: "cc-registry-default", Order: 1},
			{ID: "cc-own", Order: 2},
			nil, // 注册表里出现 nil 变体时不得 panic
		},
	}

	tests := []struct {
		name     string
		tt       *probe.TestType
		template string
		want     string
	}{
		{name: "通道自己的模板优先", tt: tt, template: "cc-own", want: "cc-own"},
		{name: "无模板时回落注册表默认值", tt: tt, template: "", want: "cc-registry-default"},
		{name: "模板已不存在时回落", tt: tt, template: "cc-removed", want: "cc-registry-default"},
		{name: "别的 service 的模板名不认", tt: tt, template: "gm-flash-arith", want: "cc-registry-default"},
		{name: "未注册的 service 给空串不 panic", tt: nil, template: "cc-own", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultTestVariant(tc.tt, tc.template); got != tc.want {
				t.Errorf("defaultTestVariant(_, %q) = %q，期望 %q", tc.template, got, tc.want)
			}
		})
	}
}

// TestAuthIndex_Rebuild_DefaultTestVariantWiring 锁死 Rebuild 确实把上面那个 helper 接进了候选，
// 而不是又退回直接读注册表默认值——本轮修的正是这一行。
func TestAuthIndex_Rebuild_DefaultTestVariantWiring(t *testing.T) {
	const service = "cctestwiring"
	registerScopedTestType(t, &probe.TestType{
		ID:             service,
		Name:           "CC Test",
		DefaultVariant: service + "-fallback",
		Variants: []*probe.PayloadVariant{
			{ID: service + "-fallback", Order: 1},
			{ID: service + "-own", Order: 2},
		},
	})

	cipher := testCipher(t)
	idx := NewAuthIndex()
	idx.Rebuild([]config.ServiceConfig{{
		Provider: "p", Service: service, Channel: "ch",
		APIKey: "sk-test-key-003", Template: service + "-own",
	}}, cipher, nil, nil)

	candidates, _ := idx.Lookup("sk-test-key-003", cipher)
	if len(candidates) != 1 {
		t.Fatalf("期望 1 个候选，得到 %d", len(candidates))
	}
	if got := candidates[0].DefaultTestVariant; got != service+"-own" {
		t.Errorf("DefaultTestVariant = %q，期望通道自身模板 %q", got, service+"-own")
	}
	// 可选项仍是该 service 的全量变体，用户可手动改选。
	if len(candidates[0].TestVariants) != 2 {
		t.Errorf("TestVariants 数量 = %d，期望 2", len(candidates[0].TestVariants))
	}
}

// TestAuthIndex_Rebuild_SelfServeHiddenVariantsStillUsable 锁死「自助不可见 ≠ 变更流程不可用」。
//
// 2026-08-21 把 native 族（cc-native-* / cx-native-*）标成 self_serve_visible:false，从公开收录
// 表单里摘掉。生产上有 10 条第一方厂商通道正跑在这些模板上，它们改 base_url / 轮换 key 必须
// 照旧走得通——变更流程要证明的是「这条通道照旧能用」，与「新申请人能不能自助选它」无关。
//
// 这条守卫针对的是一类很容易犯的后续错误：有人看到收录侧按可见性过滤，顺手把同一个判据加到
// 这里，于是那 10 条通道的默认模板一夜之间落到别的模型上、测试恒红、变更请求提交不了。
func TestAuthIndex_Rebuild_SelfServeHiddenVariantsStillUsable(t *testing.T) {
	const service = "cchiddenvariant"
	registerScopedTestType(t, &probe.TestType{
		ID:             service,
		Name:           "CC Hidden Variant",
		DefaultVariant: service + "-visible",
		Variants: []*probe.PayloadVariant{
			{ID: service + "-visible", Order: 1, SelfServeVisible: true},
			{ID: service + "-native-arith", Order: 2, SelfServeVisible: false, Native: true},
		},
	})

	cipher := testCipher(t)
	idx := NewAuthIndex()
	idx.Rebuild([]config.ServiceConfig{{
		Provider: "ark", Service: service, Channel: "ch",
		APIKey: "sk-test-key-hidden-01", Template: service + "-native-arith",
	}}, cipher, nil, nil)

	candidates, _ := idx.Lookup("sk-test-key-hidden-01", cipher)
	if len(candidates) != 1 {
		t.Fatalf("期望 1 个候选，得到 %d", len(candidates))
	}
	if got := candidates[0].DefaultTestVariant; got != service+"-native-arith" {
		t.Errorf("DefaultTestVariant = %q，期望通道自身的隐藏模板 %q——"+
			"自助不可见的模板不该影响变更流程的默认探测目标", got, service+"-native-arith")
	}
	var ids []string
	for _, v := range candidates[0].TestVariants {
		ids = append(ids, v.ID)
	}
	if len(ids) != 2 {
		t.Errorf("TestVariants = %v，期望两个都在——变更流程不按 self_serve_visible 过滤", ids)
	}
}

// registerScopedTestType 注册一个仅供本测试使用的 probe 测试类型，并在结束时把它清成惰性空壳。
// probe 注册表是包级全局且没有反注册接口，故只能把残留项的 Variants/DefaultVariant 清空，
// 让它对后续测试不再有影响（用独一无二的 service id 也是为了这个）。
func registerScopedTestType(t *testing.T, tt *probe.TestType) {
	t.Helper()
	probe.RegisterTestType(tt)
	t.Cleanup(func() {
		probe.RegisterTestType(&probe.TestType{ID: tt.ID})
	})
}
