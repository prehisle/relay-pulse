package config

import (
	"strings"
	"testing"
)

// rpdiag 按展示名 (provider 展示名/service/channel 展示名) 识别通道，model 由它的采样器选定。
// slug 不同但展示名相同，在四元组维度合法，在 rpdiag 维度却会撞成同一行。

func TestValidateMonitorDisplayKeyUniqueness_RejectsSameDisplayNameDifferentSlug(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "codekey", Service: "cc", Channel: "o-max", ProviderName: "CodeKey", ChannelName: "O-Max", Model: "claude-sonnet-5-5"},
		{Provider: "codekey", Service: "cc", Channel: "o-max-main", ProviderName: "CodeKey", ChannelName: "O-Max", Model: "claude-sonnet-5-5"},
	}}

	err := cfg.validateMonitorDisplayKeyUniqueness()
	if err == nil {
		t.Fatal("同展示键的两条监测行必须被拒")
	}
	if !strings.Contains(err.Error(), "CodeKey/cc/O-Max") {
		t.Fatalf("错误信息应点名冲突的展示键，实际 %q", err.Error())
	}
}

// 监测行探测的模型不同也要拦：rpdiag 采样器会用同一个模型去测这两条通道。
func TestValidateMonitorDisplayKeyUniqueness_RejectsEvenWhenProbeModelsDiffer(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "codekey", Service: "cc", Channel: "o-max", ProviderName: "CodeKey", ChannelName: "O-Max", Model: "claude-opus-5-5"},
		{Provider: "codekey", Service: "cc", Channel: "o-max-main", ProviderName: "CodeKey", ChannelName: "O-Max", Model: "claude-sonnet-5-5"},
	}}
	if err := cfg.validateMonitorDisplayKeyUniqueness(); err == nil {
		t.Fatal("两条不同通道同展示名，即使探测模型不同也必须被拒")
	}
}

// provider slug 不同但 provider 展示名相同：rpdiag 按名字认 provider，两条仍会撞。
func TestValidateMonitorDisplayKeyUniqueness_RejectsSameProviderDisplayNameAcrossSlugs(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "codekey", Service: "cc", Channel: "o-max", ProviderName: "CodeKey", ChannelName: "O-Max", Model: "m1"},
		{Provider: "codekey2", Service: "cc", Channel: "o-max", ProviderName: "CodeKey", ChannelName: "O-Max", Model: "m1"},
	}}
	if err := cfg.validateMonitorDisplayKeyUniqueness(); err == nil {
		t.Fatal("provider slug 不同、展示名相同的两条通道必须被拒")
	}
}

func TestValidateMonitorDisplayKeyUniqueness_FallsBackToSlugWhenDisplayEmpty(t *testing.T) {
	// 一条只写了 slug、另一条写了同名展示名：rpdiag 侧两者都叫 "O-Max"，必须拦。
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "codekey", Service: "cc", Channel: "O-Max", Model: "m1"},
		{Provider: "codekey", Service: "cc", Channel: "o-max-main", ChannelName: "O-Max", ProviderName: "codekey", Model: "m1"},
	}}
	if err := cfg.validateMonitorDisplayKeyUniqueness(); err == nil {
		t.Fatal("展示名留空回退 slug 后撞键，必须被拒")
	}
}

// 同一通道（同 slug）的多条监测行是正常的父子/多模型形态。
func TestValidateMonitorDisplayKeyUniqueness_AllowsSameChannelWithMultipleModels(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "codekey", Service: "cc", Channel: "o-max", ChannelName: "O-Max", Model: "claude-sonnet-5-5"},
		{Provider: "codekey", Service: "cc", Channel: "o-max", ChannelName: "O-Max", Model: "claude-opus-5-5"},
	}}
	if err := cfg.validateMonitorDisplayKeyUniqueness(); err != nil {
		t.Fatalf("同一通道的不同模型是正常的父子/多模型形态，不应被拒：%v", err)
	}
}

func TestValidateMonitorDisplayKeyUniqueness_AllowsSameDisplayNameAcrossServices(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "codekey", Service: "cc", Channel: "o-max", ChannelName: "O-Max", Model: "m1"},
		{Provider: "codekey", Service: "cx", Channel: "o-max", ChannelName: "O-Max", Model: "m1"},
	}}
	if err := cfg.validateMonitorDisplayKeyUniqueness(); err != nil {
		t.Fatalf("service 不同即不同身份，不应被拒：%v", err)
	}
}

func TestValidateMonitorDisplayKeyUniqueness_AllowsDifferentProviderDisplayNames(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "o-max", ProviderName: "Alpha", ChannelName: "O-Max", Model: "m1"},
		{Provider: "b", Service: "cc", Channel: "o-max", ProviderName: "Beta", ChannelName: "O-Max", Model: "m1"},
	}}
	if err := cfg.validateMonitorDisplayKeyUniqueness(); err != nil {
		t.Fatalf("provider 展示名不同即不同身份，不应被拒：%v", err)
	}
}

// 展示名可以含 "/"：若用拼接字符串做 key，下面两条会撞成 "A/cc/X/cc/Y/m"，属于误判。
func TestValidateMonitorDisplayKeyUniqueness_SlashInNameDoesNotCollide(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", ProviderName: "A", ChannelName: "X/cc/Y", Model: "m"},
		{Provider: "a", Service: "cc", Channel: "y", ProviderName: "A/cc/X", ChannelName: "Y", Model: "m"},
	}}
	if err := cfg.validateMonitorDisplayKeyUniqueness(); err != nil {
		t.Fatalf("不同身份不得因分隔符误判为重复：%v", err)
	}
}

// 下面两条走真实 normalize()：校验必须看 trim 与父子继承之后的展示名。

func TestNormalize_RejectsDisplayNamesEqualAfterTrim(t *testing.T) {
	a := minimalMonitor("codekey", "cc")
	a.Channel, a.ChannelName = "o-max", "O-Max"
	b := minimalMonitor("codekey", "cc")
	b.Channel, b.ChannelName = "o-max-main", " O-Max "
	cfg := &AppConfig{Interval: "1m", SlowLatency: "5s", Monitors: []ServiceConfig{a, b}}

	if err := cfg.normalize(); err == nil || !strings.Contains(err.Error(), "展示名重复") {
		t.Fatalf("trim 后同名的两条通道必须被拒，got=%v", err)
	}
}

func TestNormalize_ChildInheritsParentDisplayNameBeforeCheck(t *testing.T) {
	// 子行展示名为空、继承父行的 "O-Max"；另一条通道恰好以父通道的 slug "o-max" 为展示名。
	// 继承前按 slug 回退会误判，继承后两边分别是 "O-Max" 与 "o-max"，不冲突。
	parent := minimalMonitor("codekey", "cc")
	parent.Channel, parent.ChannelName, parent.Model = "o-max", "O-Max", "opus"
	child := minimalMonitor("codekey", "cc")
	child.Channel, child.Model, child.Parent = "o-max", "sonnet", "codekey/cc/o-max"
	other := minimalMonitor("codekey", "cc")
	other.Channel, other.ChannelName = "legacy", "o-max"
	cfg := &AppConfig{Interval: "1m", SlowLatency: "5s", Monitors: []ServiceConfig{parent, child, other}}

	if err := cfg.normalize(); err != nil {
		t.Fatalf("子行继承父展示名后不应冲突：%v", err)
	}
}
