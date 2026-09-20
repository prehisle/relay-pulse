package api

import (
	"testing"

	"monitor/internal/config"
)

// 模拟一个父子通道文件在 runtime 已解析后的形态：父子同 PSC，各自 Model 不同
// （AnyRouter/cc/cc：父 Haiku、子 Opus）。
func anyrouterRuntime() *config.AppConfig {
	return &config.AppConfig{
		Monitors: []config.ServiceConfig{
			// 另一个无关通道，确认 PSC 过滤生效
			{Provider: "Other", Service: "cc", Channel: "cc", Model: "X", Template: "t-x"},
			{Provider: "AnyRouter", Service: "cc", Channel: "cc", Model: "Haiku", ModelID: "md_11111111-1111-4111-8111-111111111111", Template: "cc-haiku-arith-20260506"},
			{Provider: "AnyRouter", Service: "cc", Channel: "cc", Model: "Opus", ModelID: "md_22222222-2222-4222-8222-222222222222", Parent: "AnyRouter/cc/cc", Template: "cc-opus-arith-anyrouter"},
		},
	}
}

func TestFindRawRoot(t *testing.T) {
	monitors := []config.ServiceConfig{
		{Parent: "AnyRouter/cc/cc", Template: "child"},
		{Provider: "AnyRouter", Service: "cc", Channel: "cc", Template: "parent"},
	}
	root := findRawRoot(monitors)
	if root == nil || root.Template != "parent" {
		t.Fatalf("期望返回 Parent 为空的父通道，实际 %+v", root)
	}

	if findRawRoot([]config.ServiceConfig{{Parent: "p/s/c"}}) != nil {
		t.Errorf("全是子通道时应返回 nil")
	}
}

func TestResolveRuntimeByModel(t *testing.T) {
	h := &Handler{config: anyrouterRuntime()}
	root := &config.ServiceConfig{Provider: "AnyRouter", Service: "cc", Channel: "cc"}

	t.Run("命中子通道 Opus", func(t *testing.T) {
		cfg, ok := h.resolveRuntimeByModel(root, "Opus")
		if !ok {
			t.Fatal("应命中 Opus 子通道")
		}
		if cfg.Template != "cc-opus-arith-anyrouter" || cfg.Parent == "" {
			t.Errorf("命中的应是子通道 Opus，实际 %+v", cfg)
		}
	})

	t.Run("命中父通道 Haiku", func(t *testing.T) {
		cfg, ok := h.resolveRuntimeByModel(root, "Haiku")
		if !ok || cfg.Parent != "" {
			t.Errorf("应命中父通道 Haiku，实际 ok=%v cfg=%+v", ok, cfg)
		}
	})

	t.Run("不存在的 model 不命中", func(t *testing.T) {
		if _, ok := h.resolveRuntimeByModel(root, "Sonnet"); ok {
			t.Error("不存在的 model 不应命中")
		}
	})

	t.Run("空 model / nil root 不命中", func(t *testing.T) {
		if _, ok := h.resolveRuntimeByModel(root, ""); ok {
			t.Error("空 model 不应命中")
		}
		if _, ok := h.resolveRuntimeByModel(nil, "Opus"); ok {
			t.Error("nil root 不应命中")
		}
	})

	t.Run("PSC 隔离：不跨通道命中", func(t *testing.T) {
		// Other/cc/cc 有 Model=X，但锚定 AnyRouter 时不应命中
		if _, ok := h.resolveRuntimeByModel(root, "X"); ok {
			t.Error("不应命中其他 PSC 的同名 model")
		}
	})
}

func TestBuildProbeTargets(t *testing.T) {
	root := &config.ServiceConfig{Provider: "AnyRouter", Service: "cc", Channel: "cc"}

	t.Run("runtime 命中：返回 resolved 父+子，按 PSC 过滤", func(t *testing.T) {
		h := &Handler{config: anyrouterRuntime()}
		targets := h.buildProbeTargets(root, nil)
		if len(targets) != 2 {
			t.Fatalf("期望 2 个目标（父+子），实际 %d: %+v", len(targets), targets)
		}
		if targets[0].Role != "parent" || targets[0].Model != "Haiku" {
			t.Errorf("第一个应是父通道 Haiku，实际 %+v", targets[0])
		}
		if targets[1].Role != "child" || targets[1].Model != "Opus" {
			t.Errorf("第二个应是子通道 Opus，实际 %+v", targets[1])
		}
	})

	t.Run("runtime 未命中：回退 raw（Model 可能为空）", func(t *testing.T) {
		h := &Handler{config: &config.AppConfig{}} // runtime 无该 PSC
		raw := []config.ServiceConfig{
			{Provider: "AnyRouter", Service: "cc", Channel: "cc", Template: "cc-haiku-arith-20260506"},
			{Parent: "AnyRouter/cc/cc", Template: "cc-opus-arith-anyrouter"}, // raw 子通道 Model 为空
		}
		targets := h.buildProbeTargets(root, raw)
		if len(targets) != 2 {
			t.Fatalf("期望回退 raw 2 个目标，实际 %d", len(targets))
		}
		if targets[1].Role != "child" || targets[1].Model != "" {
			t.Errorf("回退的子通道应 role=child、Model 空，实际 %+v", targets[1])
		}
	})

	t.Run("nil root 返回 nil", func(t *testing.T) {
		h := &Handler{config: anyrouterRuntime()}
		if h.buildProbeTargets(nil, nil) != nil {
			t.Error("nil root 应返回 nil")
		}
	})

	// ModelID 是前端「停用/启用某个子模型」的唯一定位键（展示名对模板驱动子行是空串、
	// 同一父下不唯一）。两条取数分支都必须带出来，缺了前端就只能退回删除子行——那正是
	// model_id 被重铸、热力图历史变孤儿的事故路径。
	t.Run("两条分支都带出 model_id", func(t *testing.T) {
		h := &Handler{config: anyrouterRuntime()}
		runtimeTargets := h.buildProbeTargets(root, nil)
		if runtimeTargets[0].ModelID != "md_11111111-1111-4111-8111-111111111111" ||
			runtimeTargets[1].ModelID != "md_22222222-2222-4222-8222-222222222222" {
			t.Errorf("runtime 分支应带出各行 model_id，实际 %+v", runtimeTargets)
		}

		hRaw := &Handler{config: &config.AppConfig{}} // runtime 无该 PSC，走 raw 回退
		raw := []config.ServiceConfig{
			{Provider: "AnyRouter", Service: "cc", Channel: "cc", ModelID: "md_33333333-3333-4333-8333-333333333333"},
			{Parent: "AnyRouter/cc/cc", ModelID: "md_44444444-4444-4444-8444-444444444444"},
		}
		rawTargets := hRaw.buildProbeTargets(root, raw)
		if rawTargets[0].ModelID != "md_33333333-3333-4333-8333-333333333333" ||
			rawTargets[1].ModelID != "md_44444444-4444-4444-8444-444444444444" {
			t.Errorf("raw 回退分支应带出各行 model_id，实际 %+v", rawTargets)
		}
	})
}
