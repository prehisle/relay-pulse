package onboarding

import "testing"

// TestNativeVendorSourceAvailable 锁定第一方厂商来源项 nat 的存在与作用域。
//
// 它承接的是「用 Anthropic/OpenAI 的协议，跑厂商自家模型」这类通道（智谱 GLM /
// 月之暗面 Kimi / MiniMax / DeepSeek / Qwen 等的兼容端点）。协议族由 service 表达、
// 模型归属由 model_vendor 正交轴表达，这里只补上收录表单里「这条线路从哪来」的选项。
//
// gm 刻意不加：Gemini 的 api（AI Studio）本身就是第一方入口，再加一条是同义重复。
func TestNativeVendorSourceAvailable(t *testing.T) {
	for _, service := range []string{"cc", "cx"} {
		if _, err := validateChannelSource(service, "nat"); err != nil {
			t.Errorf("service %q 应提供 nat 来源项: %v", service, err)
		}
	}
	if _, err := validateChannelSource("gm", "nat"); err == nil {
		t.Error("service gm 不应提供 nat 来源项（其 api 即第一方入口）")
	}
}

// TestNativeVendorSourceIsOfficialOnly 固化 nat 的类别归属：Category=official ⇒ 只允许 O（官方直连
// / 官方转售）。厂商直营既非逆向也非混合，若哪天被挪进别的类别，本测试先红。
func TestNativeVendorSourceIsOfficialOnly(t *testing.T) {
	cases := []struct {
		channelType string
		wantErr     bool
	}{
		{"O", false},
		{"R", true},
		{"M", true},
	}
	for _, tc := range cases {
		_, err := validateChannelTypeSource(tc.channelType, "cc", "nat")
		if tc.wantErr && err == nil {
			t.Errorf("channel_type %q + nat 应被拒绝", tc.channelType)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("channel_type %q + nat 应通过，实得: %v", tc.channelType, err)
		}
	}
}

// TestNativeVendorChannelCode 锁定派生出的 channel code 形状，管理员按它建 monitors.d 文件名。
func TestNativeVendorChannelCode(t *testing.T) {
	if got := deriveChannelCode("O", "nat", "main"); got != "o-nat-main" {
		t.Errorf("channel_code 应为 o-nat-main，实得 %q", got)
	}
}
