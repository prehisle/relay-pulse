package notifier

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"notifier/internal/poller"
)

// 固定时间戳：2024-01-01 00:00:00 UTC = 2024-01-01 08:00:00 CST
const (
	fixedEventTimestamp int64  = 1704067200
	fixedEventTimeCST   string = "2024-01-01 08:00:00"
)

func TestExtractModels(t *testing.T) {
	tests := []struct {
		name    string
		event   *poller.Event
		want    []string
		wantNil bool
	}{
		{
			name:    "nil 事件",
			event:   nil,
			wantNil: true,
		},
		{
			name:    "空 Meta 无 Model",
			event:   &poller.Event{Meta: map[string]any{}},
			wantNil: true,
		},
		{
			name: "Meta models 为 []string",
			event: &poller.Event{
				Meta: map[string]any{
					"models": []string{" o1 ", "", "gpt-4o"},
				},
			},
			want: []string{"gpt-4o", "o1"},
		},
		{
			name: "Meta models 为 []any 含非 string",
			event: &poller.Event{
				Meta: map[string]any{
					"models": []any{" o1 ", 42, "", "gpt-4o"},
				},
			},
			want: []string{"gpt-4o", "o1"},
		},
		{
			name: "回退 event.Model",
			event: &poller.Event{
				Model: " claude-3.5-sonnet ",
			},
			want: []string{"claude-3.5-sonnet"},
		},
		{
			name: "去重并排序",
			event: &poller.Event{
				Model: "gpt-4o",
				Meta: map[string]any{
					"models": []string{" o1 ", "claude-3.5-sonnet", "gpt-4o", "o1"},
				},
			},
			want: []string{"claude-3.5-sonnet", "gpt-4o", "o1"},
		},
		{
			name: "Meta 有 models 键但类型不支持时回退 Model",
			event: &poller.Event{
				Model: "fallback-model",
				Meta: map[string]any{
					"models": 12345, // 不支持的类型
				},
			},
			want: []string{"fallback-model"},
		},
		{
			// 这一条是本功能的核心不变量：混进业务键就等于通知里同时出现
			// 「gpt-6-astra」和它的代号「GPT」
			name: "RequestModels 优先且不并入 Meta / Model",
			event: &poller.Event{
				Model:         "GPT",
				RequestModels: []string{" gpt-6-astra ", "gpt-6-astra"},
				Meta: map[string]any{
					"models": []string{"GPT", "Claude"},
				},
			},
			want: []string{"gpt-6-astra"},
		},
		{
			name: "RequestModels 多个时去重排序",
			event: &poller.Event{
				RequestModels: []string{"gpt-6-astra", "claude-opus-4-5-20251101"},
			},
			want: []string{"claude-opus-4-5-20251101", "gpt-6-astra"},
		},
		{
			name: "RequestModels 全为空白时回退旧口径（对端为旧版 relay-pulse）",
			event: &poller.Event{
				Model:         "GPT",
				RequestModels: []string{"", "  "},
				Meta: map[string]any{
					"models": []string{"Claude"},
				},
			},
			want: []string{"Claude", "GPT"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractModels(tt.event)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("extractModels() = %#v, want nil", got)
				}
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("extractModels() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// messageFormatCase 共享 Telegram/QQ 格式化测试用例
type messageFormatCase struct {
	name         string
	event        *poller.Event
	wantTelegram string
	wantQQ       string
}

func messageFormatCases() []messageFormatCase {
	return []messageFormatCase{
		{
			// 复刻 2026-09-06 那条真实告警：修前发的是「O-web / GPT」两个标识键
			name: "展示名与真实请求模型优先于标识键",
			event: &poller.Event{
				Provider:      "saiai",
				Service:       "cx",
				Channel:       "O-web",
				Model:         "GPT",
				ChannelName:   "O-Pro",
				RequestModels: []string{"gpt-6-astra"},
				Type:          "DOWN",
				ObservedAt:    fixedEventTimestamp,
				Meta: map[string]any{
					"sub_status": "response_timeout",
				},
			},
			wantTelegram: "🔴 <b>服务不可用</b>\n\n" +
				"<b>saiai</b> / <b>cx</b> / <b>O-Pro</b>\n" +
				"模型: gpt-6-astra\n" +
				"原因: response_timeout\n\n" +
				"时间: " + fixedEventTimeCST,
			wantQQ: "🔴 服务不可用\n\n" +
				"saiai / cx / O-Pro\n" +
				"模型: gpt-6-astra\n" +
				"原因: response_timeout\n\n" +
				"时间: " + fixedEventTimeCST,
		},
		{
			name: "UP 含 channel 和单模型",
			event: &poller.Event{
				Provider:   "openai",
				Service:    "chatgpt",
				Channel:    "web",
				Model:      "gpt-4o",
				Type:       "UP",
				ObservedAt: fixedEventTimestamp,
			},
			wantTelegram: "🟢 <b>服务已恢复</b>\n\n" +
				"<b>openai</b> / <b>chatgpt</b> / <b>web</b>\n" +
				"模型: gpt-4o\n\n" +
				"时间: " + fixedEventTimeCST,
			wantQQ: "🟢 服务已恢复\n\n" +
				"openai / chatgpt / web\n" +
				"模型: gpt-4o\n\n" +
				"时间: " + fixedEventTimeCST,
		},
		{
			name: "DOWN 无 channel 含 sub_status 且 HTML 转义",
			event: &poller.Event{
				Provider:   "<Open&AI>",
				Service:    "status<prod>&v1",
				Model:      "gpt-4 <mini>&beta",
				Type:       "DOWN",
				ObservedAt: fixedEventTimestamp,
				Meta: map[string]any{
					"sub_status": "<bad & slow>",
				},
			},
			wantTelegram: "🔴 <b>服务不可用</b>\n\n" +
				"<b>&lt;Open&amp;AI&gt;</b> / <b>status&lt;prod&gt;&amp;v1</b>\n" +
				"模型: gpt-4 &lt;mini&gt;&amp;beta\n" +
				"原因: &lt;bad &amp; slow&gt;\n\n" +
				"时间: " + fixedEventTimeCST,
			wantQQ: "🔴 服务不可用\n\n" +
				"<Open&AI> / status<prod>&v1\n" +
				"模型: gpt-4 <mini>&beta\n" +
				"原因: <bad & slow>\n\n" +
				"时间: " + fixedEventTimeCST,
		},
		{
			name: "无 Type 回退 ToStatus=2 波动 + 多模型",
			event: &poller.Event{
				Provider:   "anthropic",
				Service:    "messages",
				ToStatus:   2,
				ObservedAt: fixedEventTimestamp,
				Meta: map[string]any{
					"models": []any{" o1 ", 7, "gpt-4o", "gpt-4o"},
				},
			},
			wantTelegram: "🟡 <b>服务波动</b>\n\n" +
				"<b>anthropic</b> / <b>messages</b>\n" +
				"模型: gpt-4o, o1\n\n" +
				"时间: " + fixedEventTimeCST,
			wantQQ: "🟡 服务波动\n\n" +
				"anthropic / messages\n" +
				"模型: gpt-4o, o1\n\n" +
				"时间: " + fixedEventTimeCST,
		},
		{
			name: "无 Type ToStatus=0 不可用",
			event: &poller.Event{
				Provider:   "azure",
				Service:    "openai",
				ToStatus:   0,
				ObservedAt: fixedEventTimestamp,
			},
			wantTelegram: "🔴 <b>服务不可用</b>\n\n" +
				"<b>azure</b> / <b>openai</b>\n\n" +
				"时间: " + fixedEventTimeCST,
			wantQQ: "🔴 服务不可用\n\n" +
				"azure / openai\n\n" +
				"时间: " + fixedEventTimeCST,
		},
		{
			name: "无 Type ToStatus=1 恢复",
			event: &poller.Event{
				Provider:   "test",
				Service:    "svc",
				ToStatus:   1,
				ObservedAt: fixedEventTimestamp,
			},
			wantTelegram: "🟢 <b>服务已恢复</b>\n\n" +
				"<b>test</b> / <b>svc</b>\n\n" +
				"时间: " + fixedEventTimeCST,
			wantQQ: "🟢 服务已恢复\n\n" +
				"test / svc\n\n" +
				"时间: " + fixedEventTimeCST,
		},
		{
			name: "无 Type ToStatus 未知值走 default 分支",
			event: &poller.Event{
				Provider:   "test",
				Service:    "svc",
				ToStatus:   99,
				ObservedAt: fixedEventTimestamp,
			},
			wantTelegram: "⚪ <b>状态变更</b>\n\n" +
				"<b>test</b> / <b>svc</b>\n\n" +
				"时间: " + fixedEventTimeCST,
			wantQQ: "⚪ 状态变更\n\n" +
				"test / svc\n\n" +
				"时间: " + fixedEventTimeCST,
		},
		{
			name: "回退 CreatedAt 当 ObservedAt 为 0",
			event: &poller.Event{
				Provider:  "test",
				Service:   "svc",
				Type:      "UP",
				CreatedAt: fixedEventTimestamp,
			},
			wantTelegram: "🟢 <b>服务已恢复</b>\n\n" +
				"<b>test</b> / <b>svc</b>\n\n" +
				"时间: " + fixedEventTimeCST,
			wantQQ: "🟢 服务已恢复\n\n" +
				"test / svc\n\n" +
				"时间: " + fixedEventTimeCST,
		},
	}
}

func TestTelegramChannelFormatMessage(t *testing.T) {
	ch := &telegramChannel{}

	for _, tt := range messageFormatCases() {
		t.Run(tt.name, func(t *testing.T) {
			got := ch.FormatMessage(tt.event)
			if got != tt.wantTelegram {
				t.Fatalf("FormatMessage():\ngot:  %q\nwant: %q", got, tt.wantTelegram)
			}
		})
	}
}

func TestQQChannelFormatMessage(t *testing.T) {
	ch := &qqChannel{}

	for _, tt := range messageFormatCases() {
		t.Run(tt.name, func(t *testing.T) {
			got := ch.FormatMessage(tt.event)
			if got != tt.wantQQ {
				t.Fatalf("FormatMessage():\ngot:  %q\nwant: %q", got, tt.wantQQ)
			}
		})
	}
}

func TestDisplayChannel(t *testing.T) {
	tests := []struct {
		name  string
		event *poller.Event
		want  string
	}{
		{name: "nil 事件", event: nil, want: ""},
		{
			name:  "有展示名时用展示名",
			event: &poller.Event{Channel: "O-web", ChannelName: " O-Pro "},
			want:  "O-Pro",
		},
		{
			name:  "无展示名时回退通道标识",
			event: &poller.Event{Channel: "O-web"},
			want:  "O-web",
		},
		{
			name:  "展示名全为空白时同样回退",
			event: &poller.Event{Channel: "O-web", ChannelName: "   "},
			want:  "O-web",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayChannel(tt.event); got != tt.want {
				t.Fatalf("displayChannel() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAggregateMergesRequestModels 覆盖多模型通道：窗口内每个事件各带自己的
// request_model，合并后的通知必须列全，而不是只留基准事件那一个。
func TestAggregateMergesRequestModels(t *testing.T) {
	s := &Sender{
		aggWindow: time.Hour, // 足够长，测试期间不会自行 flush
		aggBuf:    make(map[aggregateKey]*eventAggregate),
	}

	events := []*poller.Event{
		{
			Provider: "saiai", Service: "cc", Channel: "o-max",
			Model: "Opus", ChannelName: "O-Max",
			RequestModels: []string{"claude-opus-5"},
			Type:          "DOWN",
		},
		{
			Provider: "saiai", Service: "cc", Channel: "o-max",
			Model: "Sonnet", ChannelName: "O-Max",
			RequestModels: []string{"claude-sonnet-5"},
			Type:          "DOWN",
		},
	}
	for _, e := range events {
		if err := s.HandleEvent(context.Background(), e); err != nil {
			t.Fatalf("HandleEvent() error = %v", err)
		}
	}

	key := aggregateKey{Provider: "saiai", Service: "cc", Channel: "o-max", EventType: "DOWN"}
	s.aggMu.Lock()
	agg := s.aggBuf[key]
	s.aggMu.Unlock()
	if agg == nil {
		t.Fatal("聚合缓冲区缺失")
	}
	if agg.timer != nil {
		agg.timer.Stop()
	}

	merged := agg.mergedEvent()

	wantRendered := []string{"claude-opus-5", "claude-sonnet-5"}
	if !slices.Equal(merged.RequestModels, wantRendered) {
		t.Fatalf("merged.RequestModels = %#v, want %#v", merged.RequestModels, wantRendered)
	}
	if !slices.Equal(extractModels(&merged), wantRendered) {
		t.Fatalf("extractModels(merged) = %#v, want %#v", extractModels(&merged), wantRendered)
	}
	if got := displayChannel(&merged); got != "O-Max" {
		t.Fatalf("displayChannel(merged) = %q, want O-Max", got)
	}

	// 业务键那一路保持原样：它仍是 model 展示名/DB 键的口径，没有被 request_model 污染
	wantModels := []string{"Opus", "Sonnet"}
	if !slices.Equal(merged.Meta["models"].([]string), wantModels) {
		t.Fatalf("merged.Meta[models] = %#v, want %#v", merged.Meta["models"], wantModels)
	}
}

// TestAggregateWithoutRequestModels 固定旧版 relay-pulse 对端的行为：
// 没有 request_models 时聚合结果必须与本次改动前逐字一致。
func TestAggregateWithoutRequestModels(t *testing.T) {
	s := &Sender{
		aggWindow: time.Hour,
		aggBuf:    make(map[aggregateKey]*eventAggregate),
	}

	for _, model := range []string{"Opus", "Sonnet"} {
		e := &poller.Event{Provider: "p", Service: "s", Channel: "c", Model: model, Type: "UP"}
		if err := s.HandleEvent(context.Background(), e); err != nil {
			t.Fatalf("HandleEvent() error = %v", err)
		}
	}

	s.aggMu.Lock()
	agg := s.aggBuf[aggregateKey{Provider: "p", Service: "s", Channel: "c", EventType: "UP"}]
	s.aggMu.Unlock()
	if agg == nil {
		t.Fatal("聚合缓冲区缺失")
	}
	if agg.timer != nil {
		agg.timer.Stop()
	}

	merged := agg.mergedEvent()
	want := []string{"Opus", "Sonnet"}
	if !slices.Equal(merged.Meta["models"].([]string), want) {
		t.Fatalf("merged.Meta[models] = %#v, want %#v", merged.Meta["models"], want)
	}
	if !slices.Equal(extractModels(&merged), want) {
		t.Fatalf("extractModels(merged) = %#v, want %#v", extractModels(&merged), want)
	}
	if got := displayChannel(&merged); got != "c" {
		t.Fatalf("displayChannel(merged) = %q, want c", got)
	}
}

// TestRenderFromRealWirePayload 端到端固定跨服务契约：这段 JSON 是
// relay-pulse `internal/api` 的 EventItem 实际 marshal 出来的字节
// （见那边的 TestEventItemWireShape），从它解码后必须渲染出展示名与真实模型。
// 任何一侧改错 json tag，这里会红。
func TestRenderFromRealWirePayload(t *testing.T) {
	const payload = `{"id":1,"provider":"saiai","service":"cx","channel":"O-web",` +
		`"model":"GPT","channel_name":"O-Pro","request_models":["gpt-6-astra"],` +
		`"type":"DOWN","from_status":1,"to_status":0,"trigger_record_id":0,` +
		`"observed_at":1704067200,"created_at":0,"meta":{"sub_status":"response_timeout"}}`

	var event poller.Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("解码 /api/events 载荷失败: %v", err)
	}

	want := "🔴 服务不可用\n\nsaiai / cx / O-Pro\n模型: gpt-6-astra\n" +
		"原因: response_timeout\n\n时间: " + fixedEventTimeCST
	if got := (&qqChannel{}).FormatMessage(&event); got != want {
		t.Fatalf("FormatMessage():\ngot:  %q\nwant: %q", got, want)
	}
}
