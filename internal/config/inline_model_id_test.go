package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// writeInlineConfig 写一份最小 config.yaml 到临时目录并返回其路径，供 Loader.Load 端到端测试。
func writeInlineConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// --- 纯函数派生逻辑 ---

// TestDeriveInlineModelID_ValidFormat 派生结果必须是带 md_ 前缀的合法 model id（能过运行时闸）。
func TestDeriveInlineModelID_ValidFormat(t *testing.T) {
	id := deriveInlineModelID(ServiceConfig{Provider: "acme", Service: "cc", Channel: "vip", Model: "Haiku"})
	if !IsValidModelID(id) {
		t.Fatalf("derived id not a valid model id: %q", id)
	}
}

// TestDeriveInlineModelID_Deterministic 同一 PSCM 多次派生必须得到同一 id——
// 这是"跨重启/热更新历史不断档"的根本前提。
func TestDeriveInlineModelID_Deterministic(t *testing.T) {
	m := ServiceConfig{Provider: "acme", Service: "cc", Channel: "vip", Model: "Haiku"}
	if a, b := deriveInlineModelID(m), deriveInlineModelID(m); a != b {
		t.Fatalf("derivation not deterministic: %q != %q", a, b)
	}
}

// TestDeriveInlineModelID_DistinctPerPSCM PSCM 任一段不同 → id 不同（同通道多模型不撞键）。
func TestDeriveInlineModelID_DistinctPerPSCM(t *testing.T) {
	base := ServiceConfig{Provider: "acme", Service: "cc", Channel: "vip", Model: "Haiku"}
	variants := []ServiceConfig{
		{Provider: "acme", Service: "cc", Channel: "vip", Model: "Sonnet"}, // 仅 model 不同（多模型子通道）
		{Provider: "acme", Service: "cc", Channel: "vip2", Model: "Haiku"}, // 仅 channel 不同
		{Provider: "acme", Service: "gm", Channel: "vip", Model: "Haiku"},  // 仅 service 不同
		{Provider: "beta", Service: "cc", Channel: "vip", Model: "Haiku"},  // 仅 provider 不同
	}
	baseID := deriveInlineModelID(base)
	seen := map[string]bool{baseID: true}
	for _, v := range variants {
		id := deriveInlineModelID(v)
		if seen[id] {
			t.Fatalf("PSCM %+v collided with an earlier id %q", v, id)
		}
		seen[id] = true
	}
}

// TestDeriveInlineModelID_LengthPrefixDisambiguates 字段本身含 "/" 时，
// 长度前缀编码必须让不同 PSCM 拆分产生不同 id（防裸斜杠拼接歧义）。
func TestDeriveInlineModelID_LengthPrefixDisambiguates(t *testing.T) {
	a := deriveInlineModelID(ServiceConfig{Provider: "a/b", Service: "c", Channel: "d", Model: "e"})
	b := deriveInlineModelID(ServiceConfig{Provider: "a", Service: "b", Channel: "c", Model: "d/e"})
	if a == b {
		t.Fatalf("ambiguous slash encoding: distinct PSCM produced same id %q", a)
	}
}

// --- Loader.Load 集成（复现并锁定新手 bug）---

// TestLoad_InlineMonitorsGetDerivedModelID 核心回归：config.yaml 内联监测行不写 model_id
// （官方 config.yaml.example 正是如此）时，Load 后每行都应有合法 model_id 且过运行时闸。
func TestLoad_InlineMonitorsGetDerivedModelID(t *testing.T) {
	path := writeInlineConfig(t, `monitors:
  - provider: acme
    service: cc
    channel: vip
    model: Haiku
    base_url: https://a.example.com
    method: POST
  - provider: beta
    service: gm
    channel: direct
    model: Flash
    base_url: https://b.example.com
    method: POST
`)
	cfg, err := NewLoader().Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Monitors) != 2 {
		t.Fatalf("expected 2 monitors, got %d", len(cfg.Monitors))
	}
	for i, m := range cfg.Monitors {
		if !IsValidModelID(m.ModelID) {
			t.Errorf("monitor[%d] %s/%s/%s: model_id not derived: %q", i, m.Provider, m.Service, m.Channel, m.ModelID)
		}
	}
	if err := CheckRuntimeModelIDs(cfg.Monitors); err != nil {
		t.Fatalf("runtime gate should pass after inline derivation, got %v", err)
	}
}

// TestLoad_DerivedModelIDStableAcrossReload 同一文件两次 Load → 派生 id 必须逐行一致。
func TestLoad_DerivedModelIDStableAcrossReload(t *testing.T) {
	body := `monitors:
  - provider: acme
    service: cc
    channel: vip
    model: Haiku
    base_url: https://a.example.com
    method: POST
`
	path := writeInlineConfig(t, body)
	first, err := NewLoader().Load(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := NewLoader().Load(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if first.Monitors[0].ModelID != second.Monitors[0].ModelID {
		t.Fatalf("derived id unstable across reload: %q != %q", first.Monitors[0].ModelID, second.Monitors[0].ModelID)
	}
}

// TestLoad_ExplicitModelIDPreserved 已显式写了合法 model_id 的行绝不被派生覆盖。
func TestLoad_ExplicitModelIDPreserved(t *testing.T) {
	explicit := NewModelID()
	path := writeInlineConfig(t, `monitors:
  - provider: acme
    service: cc
    channel: vip
    model: Haiku
    model_id: `+explicit+`
    base_url: https://a.example.com
    method: POST
`)
	cfg, err := NewLoader().Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Monitors[0].ModelID != explicit {
		t.Fatalf("explicit model_id overwritten: got %q want %q", cfg.Monitors[0].ModelID, explicit)
	}
}

// TestLoad_ReorderPreservesDerivedID 交换 config.yaml 中两行顺序，同一 PSCM 的派生 id 不变
// （id 跟 PSCM 走、不跟行序走——重排/中间插行不断历史）。
func TestLoad_ReorderPreservesDerivedID(t *testing.T) {
	mon1 := `  - provider: acme
    service: cc
    channel: vip
    model: Haiku
    base_url: https://a.example.com
    method: POST
`
	mon2 := `  - provider: beta
    service: gm
    channel: direct
    model: Flash
    base_url: https://b.example.com
    method: POST
`
	cfgA, err := NewLoader().Load(writeInlineConfig(t, "monitors:\n"+mon1+mon2))
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	cfgB, err := NewLoader().Load(writeInlineConfig(t, "monitors:\n"+mon2+mon1))
	if err != nil {
		t.Fatalf("load B: %v", err)
	}
	idByPSC := func(monitors []ServiceConfig, provider string) string {
		for _, m := range monitors {
			if m.Provider == provider {
				return m.ModelID
			}
		}
		return ""
	}
	if idByPSC(cfgA.Monitors, "acme") != idByPSC(cfgB.Monitors, "acme") {
		t.Errorf("acme id changed after reorder")
	}
	if idByPSC(cfgA.Monitors, "beta") != idByPSC(cfgB.Monitors, "beta") {
		t.Errorf("beta id changed after reorder")
	}
}

// TestLoad_MonitorsDirIDsUntouched 混合部署：config.yaml 内联行获派生 id，
// 而 monitors.d/ 文件里持久化的 model_id 原样保留（monitors.d 语义完全不动）。
func TestLoad_MonitorsDirIDsUntouched(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`monitors:
  - provider: inline
    service: cc
    channel: vip
    model: Haiku
    base_url: https://a.example.com
    method: POST
`), 0600); err != nil {
		t.Fatal(err)
	}
	mdDir := filepath.Join(dir, MonitorsDirName)
	if err := os.MkdirAll(mdDir, 0755); err != nil {
		t.Fatal(err)
	}
	persisted := NewModelID()
	if err := os.WriteFile(filepath.Join(mdDir, "ext--cc--vip.yaml"), []byte(`metadata:
  revision: 1
  channel_id: ch_11111111-1111-4111-8111-111111111111
monitors:
  - provider: ext
    service: cc
    channel: vip
    model: Sonnet
    model_id: `+persisted+`
    base_url: https://c.example.com
    method: POST
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var inlineID, extID string
	for _, m := range cfg.Monitors {
		switch m.Provider {
		case "inline":
			inlineID = m.ModelID
		case "ext":
			extID = m.ModelID
		}
	}
	if !IsValidModelID(inlineID) {
		t.Errorf("inline monitor not derived: %q", inlineID)
	}
	if extID != persisted {
		t.Errorf("monitors.d persisted model_id changed: got %q want %q", extID, persisted)
	}
}

// TestLoad_MonitorsDirMissingIDStaysEmpty 锁住"绝不给 monitors.d 行派生 id"：monitors.d 文件
// 缺 model_id 时，Load 后该行仍为空、运行时闸仍 fail-closed（保留 backfillids 持久化流程语义）。
func TestLoad_MonitorsDirMissingIDStaysEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`monitors:
  - provider: inline
    service: cc
    channel: vip
    model: Haiku
    base_url: https://a.example.com
    method: POST
`), 0600); err != nil {
		t.Fatal(err)
	}
	mdDir := filepath.Join(dir, MonitorsDirName)
	if err := os.MkdirAll(mdDir, 0755); err != nil {
		t.Fatal(err)
	}
	// monitors.d 文件故意缺 model_id（无 channel_id 亦可，loadMonitorsDir 允许空）。
	if err := os.WriteFile(filepath.Join(mdDir, "ext--cc--vip.yaml"), []byte(`metadata:
  revision: 1
monitors:
  - provider: ext
    service: cc
    channel: vip
    model: Sonnet
    base_url: https://c.example.com
    method: POST
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var extID string
	for _, m := range cfg.Monitors {
		if m.Provider == "ext" {
			extID = m.ModelID
		}
	}
	if extID != "" {
		t.Errorf("monitors.d missing id must NOT be derived, got %q", extID)
	}
	if err := CheckRuntimeModelIDs(cfg.Monitors); err == nil {
		t.Error("runtime gate must still reject a monitors.d row missing model_id")
	}
}

// TestLoad_HandAuthoredIDCollidingWithDerivedRejected 锁住派生后的唯一性兜底：另一行手工
// 填写的 model_id 恰等于某缺 id 内联行将派生出的 v5 时，Load 必须因重复 fail-closed。
func TestLoad_HandAuthoredIDCollidingWithDerivedRejected(t *testing.T) {
	// 先算出 acme/cc/vip/Haiku 会派生的 id，再把它手填到另一行制造冲突。
	collide := deriveInlineModelID(ServiceConfig{Provider: "acme", Service: "cc", Channel: "vip", Model: "Haiku"})
	path := writeInlineConfig(t, `monitors:
  - provider: acme
    service: cc
    channel: vip
    model: Haiku
    base_url: https://a.example.com
    method: POST
  - provider: beta
    service: cc
    channel: vip
    model: Sonnet
    model_id: `+collide+`
    base_url: https://b.example.com
    method: POST
`)
	_, err := NewLoader().Load(path)
	if err == nil {
		t.Fatal("expected Load to reject derived-vs-explicit model_id collision")
	}
}

// TestLoad_TemplateProvidedModelGetsDerivedID 复刻官方 config.yaml.example 的真实路径：
// 监测行不写 model（model 由 template 填充）、不写 model_id。Load 后 model 应由模板填好、
// 且据"解析后的" PSCM 派生出合法 model_id 过运行时闸。用真实 repo 模板文件做端到端。
func TestLoad_TemplateProvidedModelGetsDerivedID(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cc-haiku-arith", "gm-flash-arith"} {
		src, err := os.ReadFile(filepath.Join("..", "..", "templates", name+".json"))
		if err != nil {
			t.Fatalf("read repo template %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmplDir, name+".json"), src, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	// 与 config.yaml.example 同构：无 model、无 model_id。
	if err := os.WriteFile(cfgPath, []byte(`monitors:
  - provider: "88code"
    service: "cc"
    channel: "vip3"
    base_url: "https://api.88code.com"
    template: "cc-haiku-arith"
    api_key: "sk-xxxxxxxx"
  - provider: "google"
    service: "gm"
    channel: "direct"
    base_url: "https://generativelanguage.googleapis.com"
    template: "gm-flash-arith"
    api_key: "AIza..."
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("load official-example-shaped config: %v", err)
	}
	for i, m := range cfg.Monitors {
		if m.Model == "" {
			t.Errorf("monitor[%d]: model not filled from template", i)
		}
		if !IsValidModelID(m.ModelID) {
			t.Errorf("monitor[%d] %s/%s/%s: model_id not derived: %q", i, m.Provider, m.Service, m.Channel, m.ModelID)
		}
	}
	if err := CheckRuntimeModelIDs(cfg.Monitors); err != nil {
		t.Fatalf("runtime gate must pass for official-example-shaped config, got %v", err)
	}
}

// TestDeriveInlineModelID_IsUUIDv5 派生走 uuidv5（SHA1 命名空间），与随机 v4 生成路径区分。
func TestDeriveInlineModelID_IsUUIDv5(t *testing.T) {
	id := deriveInlineModelID(ServiceConfig{Provider: "acme", Service: "cc", Channel: "vip", Model: "Haiku"})
	parsed, err := uuid.Parse(id[len(modelIDPrefix):])
	if err != nil {
		t.Fatalf("derived body not a uuid: %v", err)
	}
	if parsed.Version() != 5 {
		t.Errorf("expected uuid v5, got v%d", parsed.Version())
	}
}
