package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"monitor/internal/config"
)

// newDisplayTestHandler 构造一个只带 Monitors 的 Handler，够 buildChannelDisplayIndex 用。
func newDisplayTestHandler(monitors ...config.ServiceConfig) *Handler {
	return &Handler{config: &config.AppConfig{Monitors: monitors}}
}

// TestEnrichEventDisplay 覆盖 /api/events 的展示名富化：事件表里存的是标识键
// （channel=O-web、model=GPT），下发给通知端的必须是站点上那两个串。
func TestEnrichEventDisplay(t *testing.T) {
	idx := newDisplayTestHandler(
		config.ServiceConfig{
			Provider: "saiai", Service: "cx", Channel: "O-web", ChannelName: "O-Pro",
			Model: "GPT", RequestModel: "gpt-6-astra",
		},
		config.ServiceConfig{
			Provider: "saiai", Service: "cc", Channel: "o-max", ChannelName: "O-Max",
			Model: "Opus", RequestModel: "claude-opus-5",
		},
		config.ServiceConfig{
			Provider: "saiai", Service: "cc", Channel: "o-max", ChannelName: "O-Max",
			Model: "Sonnet", RequestModel: "claude-sonnet-5",
		},
		// 通道名与标识相同：没有展示名可给，ChannelName 应留空
		config.ServiceConfig{
			Provider: "acme", Service: "cc", Channel: "main", ChannelName: "main",
			Model: "Haiku", RequestModel: "claude-haiku-4-5",
		},
		// native 族：request_model 为空，model 本身就是完整模型 ID
		config.ServiceConfig{
			Provider: "ark", Service: "cc", Channel: "o-nat", Model: "glm-5.2",
		},
		// loader 不 trim model，配置里混进空格时两侧必须同步 trim 才能 join 上
		config.ServiceConfig{
			Provider: "sloppy", Service: "cc", Channel: "main", ChannelName: "Sloppy",
			Model: " Opus ", RequestModel: "claude-opus-5",
		},
		// 首行没写展示名、后续层才写：应取首个非空而不是停在空
		config.ServiceConfig{
			Provider: "late", Service: "cc", Channel: "main", Model: "Opus",
			RequestModel: "claude-opus-5",
		},
		config.ServiceConfig{
			Provider: "late", Service: "cc", Channel: "main", ChannelName: "Late-Pro",
			Model: "Sonnet", RequestModel: "claude-sonnet-5",
		},
	).buildChannelDisplayIndex()

	tests := []struct {
		name              string
		item              EventItem
		wantChannelName   string
		wantRequestModels []string
	}{
		{
			name:              "模型级事件映射到真实请求模型",
			item:              EventItem{Provider: "saiai", Service: "cx", Channel: "O-web", Model: "GPT"},
			wantChannelName:   "O-Pro",
			wantRequestModels: []string{"gpt-6-astra"},
		},
		{
			name: "通道级事件映射 Meta[models] 全部成员（DB 往返后是 []any）",
			item: EventItem{
				Provider: "saiai", Service: "cc", Channel: "o-max", Model: "Opus",
				Meta: map[string]any{"models": []any{"Opus", "Sonnet", 42, ""}},
			},
			wantChannelName:   "O-Max",
			wantRequestModels: []string{"claude-opus-5", "claude-sonnet-5"},
		},
		{
			name: "Meta[models] 为 []string 时同样映射，并与触发 model 取并集",
			item: EventItem{
				Provider: "saiai", Service: "cc", Channel: "o-max", Model: "Opus",
				Meta: map[string]any{"models": []string{"Sonnet"}},
			},
			wantChannelName:   "O-Max",
			wantRequestModels: []string{"claude-opus-5", "claude-sonnet-5"},
		},
		{
			name:              "通道展示名与标识相同则不下发",
			item:              EventItem{Provider: "acme", Service: "cc", Channel: "main", Model: "Haiku"},
			wantChannelName:   "",
			wantRequestModels: []string{"claude-haiku-4-5"},
		},
		{
			name:              "request_model 为空时回退 model（native 族）",
			item:              EventItem{Provider: "ark", Service: "cc", Channel: "o-nat", Model: "glm-5.2"},
			wantChannelName:   "",
			wantRequestModels: []string{"glm-5.2"},
		},
		{
			name:              "配置 model 带首尾空格时照样 join 得上",
			item:              EventItem{Provider: "sloppy", Service: "cc", Channel: "main", Model: " Opus "},
			wantChannelName:   "Sloppy",
			wantRequestModels: []string{"claude-opus-5"},
		},
		{
			name:              "展示名取同 PSC 首个非空，而非停在首行的空值",
			item:              EventItem{Provider: "late", Service: "cc", Channel: "main", Model: "Opus"},
			wantChannelName:   "Late-Pro",
			wantRequestModels: []string{"claude-opus-5"},
		},
		{
			name:              "通道已下架：展示名留空、模型回退业务键",
			item:              EventItem{Provider: "gone", Service: "cx", Channel: "old", Model: "GPT"},
			wantChannelName:   "",
			wantRequestModels: []string{"GPT"},
		},
		{
			name:              "通道还在但模型已换名：只有模型回退",
			item:              EventItem{Provider: "saiai", Service: "cx", Channel: "O-web", Model: "Codex"},
			wantChannelName:   "O-Pro",
			wantRequestModels: []string{"Codex"},
		},
		{
			name:              "两个字段都无从可查时不产生任何展示字段",
			item:              EventItem{Provider: "gone", Service: "cx", Channel: "old"},
			wantChannelName:   "",
			wantRequestModels: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := tt.item
			idx.enrich(&item)

			if item.ChannelName != tt.wantChannelName {
				t.Fatalf("ChannelName = %q, want %q", item.ChannelName, tt.wantChannelName)
			}
			if !slices.Equal(item.RequestModels, tt.wantRequestModels) {
				t.Fatalf("RequestModels = %#v, want %#v", item.RequestModels, tt.wantRequestModels)
			}
		})
	}
}

// TestBuildChannelDisplayIndexIsCaseSensitive 钉死索引键用配置原值。
// 四元组唯一性（config.validateMonitorUniqueness）就是原值比较，因此
// PSC 或 model 只差大小写的两行是合法且不同的监测项；一旦有人"顺手"给
// 索引键加上 ToLower，它们会被折叠成一条，request_model 取谁看遍历顺序。
func TestBuildChannelDisplayIndexIsCaseSensitive(t *testing.T) {
	idx := newDisplayTestHandler(
		config.ServiceConfig{Provider: "p", Service: "s", Channel: "c", Model: "GPT", RequestModel: "gpt-upper"},
		config.ServiceConfig{Provider: "p", Service: "s", Channel: "c", Model: "gpt", RequestModel: "gpt-lower"},
		config.ServiceConfig{Provider: "p", Service: "s", Channel: "C", Model: "GPT", RequestModel: "gpt-other-channel"},
	).buildChannelDisplayIndex()

	cases := []struct {
		channel, model, want string
	}{
		{"c", "GPT", "gpt-upper"},
		{"c", "gpt", "gpt-lower"},
		{"C", "GPT", "gpt-other-channel"},
	}
	for _, tc := range cases {
		item := EventItem{Provider: "p", Service: "s", Channel: tc.channel, Model: tc.model}
		idx.enrich(&item)
		if !slices.Equal(item.RequestModels, []string{tc.want}) {
			t.Fatalf("channel=%q model=%q → %#v, want [%q]", tc.channel, tc.model, item.RequestModels, tc.want)
		}
	}
}

// TestEventItemWireShape 钉住下发给 notifier 的 JSON 键名。这两个键是跨服务契约
// （notifier 是独立部署的另一个进程），任何一边打错 tag 都不会报错——只会让通知
// 悄悄退回显示标识键，正是本次要修的症状。
func TestEventItemWireShape(t *testing.T) {
	enriched, err := json.Marshal(EventItem{
		ID: 1, Provider: "saiai", Service: "cx", Channel: "O-web", Model: "GPT",
		ChannelName: "O-Pro", RequestModels: []string{"gpt-6-astra"},
		Type: "DOWN",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"channel_name":"O-Pro"`, `"request_models":["gpt-6-astra"]`} {
		if !strings.Contains(string(enriched), want) {
			t.Fatalf("wire 缺少 %s：\n%s", want, enriched)
		}
	}

	// 富化不到时两个键必须完全不出现（对旧 notifier 是逐字节 additive）
	bare, err := json.Marshal(EventItem{ID: 1, Provider: "saiai", Service: "cx", Type: "DOWN"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, unwanted := range []string{"channel_name", "request_models"} {
		if strings.Contains(string(bare), unwanted) {
			t.Fatalf("空值不该出现在 wire 上（%s）：\n%s", unwanted, bare)
		}
	}
}
