package onboarding

import (
	"context"
	"strings"
	"testing"
)

// newReplayTestRequest 构造一份能一路走到 Save 的最小合法提交。
func newReplayTestRequest(svc *Service, jobID string) *SubmitRequest {
	const (
		apiKey = "sk-replay-test-key-000001"
		apiURL = "https://api.replay-example.com"
	)
	req := &SubmitRequest{
		AgreementAccepted: true,
		ProviderName:      "ReplayProv",
		WebsiteURL:        "https://replay-example.com",
		Category:          "commercial",
		ServiceType:       "cc",
		TemplateName:      "cc-haiku-arith",
		SponsorLevel:      "pulse",
		ChannelType:       "O",
		ChannelSource:     "max",
		ChannelGroup:      "main",
		BaseURL:           apiURL,
		APIKey:            apiKey,
		TestJobID:         jobID,
		TestType:          "cc",
		TestAPIURL:        apiURL,
	}
	req.TestProof = svc.IssueProof(jobID, req.TestType, apiURL, apiKey)
	return req
}

// TestSubmit_TestProofIsSingleUse 锁定 proof 一次性消费。
//
// proof 是无状态 HMAC，TTL 内可无限重放；生产实证一次探测被兑换成 7 条提交。
// 这里断言同一 test_job_id 的第二次提交必须被拒，且不产生第二条记录。
func TestSubmit_TestProofIsSingleUse(t *testing.T) {
	svc := newSubmitTestService(t)
	ctx := context.Background()
	req := newReplayTestRequest(svc, "probe-single-use-0001")

	if _, err := svc.Submit(ctx, req, "1.2.3.4"); err != nil {
		t.Fatalf("首次提交应成功: %v", err)
	}

	_, err := svc.Submit(ctx, req, "5.6.7.8")
	if err == nil {
		t.Fatal("复用同一 test_job_id 的提交应被拒绝，实际 nil")
	}
	if !strings.Contains(err.Error(), "已被使用") {
		t.Fatalf("期望 proof 已被使用的拒因，实际: %v", err)
	}

	store := svc.store.(*SQLStore)
	_, total, listErr := store.List(ctx, "", "", 50, 0)
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if total != 1 {
		t.Fatalf("重放被拒后记录数=%d，期望 1", total)
	}
}

// TestSubmit_DistinctTestJobIDsBothAccepted 反向守卫：一次性消费不得误伤正常的多次提交
// （每次都真做了一次探测、拿到各自的 job_id）。
func TestSubmit_DistinctTestJobIDsBothAccepted(t *testing.T) {
	svc := newSubmitTestService(t)
	ctx := context.Background()

	for _, jobID := range []string{"probe-distinct-a", "probe-distinct-b"} {
		if _, err := svc.Submit(ctx, newReplayTestRequest(svc, jobID), "1.2.3.4"); err != nil {
			t.Fatalf("job_id=%s 提交应成功: %v", jobID, err)
		}
	}

	store := svc.store.(*SQLStore)
	if _, total, err := store.List(ctx, "", "", 50, 0); err != nil {
		t.Fatalf("List: %v", err)
	} else if total != 2 {
		t.Fatalf("两次不同探测的提交数=%d，期望 2", total)
	}
}
