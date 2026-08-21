package onboarding

import (
	"context"
	"strings"
	"testing"
	"time"

	"monitor/internal/config"
)

// newSubmitTestService 在 newTestService 的最小依赖之上补 MaxPerIPPerDay，使 Submit 能越过 IP 限流
// 抵达 base_url/host 校验（newTestService 的限额为零值 0，任何提交都先被限流拦下）。
func newSubmitTestService(t *testing.T) *Service {
	t.Helper()
	store := newTestStore(t)
	cfg := &config.OnboardingConfig{
		EncryptionKey:    testKey(),
		ProofSecret:      "test-proof-secret",
		ProofTTLDuration: 5 * time.Minute,
		MaxPerIPPerDay:   5,
	}
	svc, err := NewService(store, cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// TestSubmit_BaseURLPortMustMatchTestAPIURL 锁定 onboarding 侧的 host/port 一致性（与 change 流程对称、
// 共享 urlutil.SameHostPort）：base_url 与 test_api_url 同 host 但端口不同时须被拒——防止在一个端口测出
// proof、却把 base_url 收录成同 host 另一端口的绕过（此前只比 hostname、漏比端口）。
func TestSubmit_BaseURLPortMustMatchTestAPIURL(t *testing.T) {
	svc := newSubmitTestService(t)
	_, err := svc.Submit(context.Background(), &SubmitRequest{
		AgreementAccepted: true,
		ProviderName:      "Prov",
		ServiceType:       "cc",
		ChannelType:       "O",
		ChannelSource:     "max",
		ChannelGroup:      "main",
		BaseURL:           "https://api.new.com:9999",
		TestAPIURL:        "https://api.new.com",
	}, "1.2.3.4")
	if err == nil {
		t.Fatal("同 host 不同端口应被拒，实际 nil")
	}
	if !strings.Contains(err.Error(), "必须完全一致") {
		t.Fatalf("期望同一接入点校验拒因，实际: %v", err)
	}
}

// TestSubmit_BaseURLPathMustMatchTestAPIURL 锁定路径替换这条既有缺口。
//
// 只比 host/port 时，可以拿 https://edge/good 测出 proof、再提交 base_url=https://edge/other：
// 两者 host/port 相同，却是完全不同的上游——中转商的多条线路恰恰常按路径区分，等于绕开了
// 「先证明这条线路可用」。同时确认尾部斜杠不误伤（用户在表单里多敲一个 / 是常事）。
func TestSubmit_BaseURLPathMustMatchTestAPIURL(t *testing.T) {
	t.Run("同 host 不同路径应被拒", func(t *testing.T) {
		svc := newSubmitTestService(t)
		_, err := svc.Submit(context.Background(), &SubmitRequest{
			AgreementAccepted: true,
			ProviderName:      "Prov",
			ServiceType:       "cc",
			TemplateName:      "cc-haiku-arith",
			ChannelType:       "O",
			ChannelSource:     "max",
			ChannelGroup:      "main",
			BaseURL:           "https://edge.example.com/other",
			TestAPIURL:        "https://edge.example.com/good",
		}, "1.2.3.4")
		if err == nil || !strings.Contains(err.Error(), "必须完全一致") {
			t.Fatalf("同 host 不同路径应被拒，实际: %v", err)
		}
	})

	t.Run("仅尾部斜杠之差不应被拒", func(t *testing.T) {
		svc := newSubmitTestService(t)
		_, err := svc.Submit(context.Background(), &SubmitRequest{
			AgreementAccepted: true,
			ProviderName:      "Prov",
			ServiceType:       "cc",
			TemplateName:      "cc-haiku-arith",
			ChannelType:       "O",
			ChannelSource:     "max",
			ChannelGroup:      "main",
			BaseURL:           "https://edge.example.com/good/",
			TestAPIURL:        "https://edge.example.com/good",
		}, "1.2.3.4")
		// 该组合会继续走到 proof 校验（本用例不构造 proof），故只断言不是被 URL 校验挡下的
		if err != nil && strings.Contains(err.Error(), "必须完全一致") {
			t.Fatalf("仅尾部斜杠之差不应被 URL 校验拒绝，实际: %v", err)
		}
	})
}
