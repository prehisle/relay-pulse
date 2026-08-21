package onboarding

import (
	"context"
	"monitor/internal/apikey"
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
	// 绑定与 Submit 侧同口径：模板名进 claims，行级模型为空（cc-haiku-arith 非 native 族）
	req.TestProof = svc.IssueProof(apikey.ProofClaims{
		JobID:    jobID,
		TestType: req.TestType,
		APIURL:   apiURL,
		Variant:  req.TemplateName,
	}, apiKey)
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

// TestSubmit_SponsorLevelWhitelist 锁定赞助等级白名单。
//
// 2026-07-25 的探测流量里有一条自选 sponsor_level=core（最高付费档）的提交：
// 此前只黑名单挡了 public/signal，而该值会被 BuildServiceConfigFromSubmission
// 直接灌进上架配置——管理员不改字段直接发布，自封的付费徽章即刻生效。
func TestSubmit_SponsorLevelWhitelist(t *testing.T) {
	ctx := context.Background()

	for _, level := range []string{"core", "backbone", "beacon", "public", "signal", "bogus"} {
		t.Run("拒绝_"+level, func(t *testing.T) {
			svc := newSubmitTestService(t)
			req := newReplayTestRequest(svc, "probe-sponsor-"+level)
			req.SponsorLevel = level

			_, err := svc.Submit(ctx, req, "1.2.3.4")
			if err == nil {
				t.Fatalf("sponsor_level=%q 应被拒绝，实际 nil", level)
			}
			if !strings.Contains(err.Error(), "赞助等级") {
				t.Fatalf("期望赞助等级拒因，实际: %v", err)
			}
		})
	}

	for _, level := range []string{"", "pulse"} {
		t.Run("接受_"+level, func(t *testing.T) {
			svc := newSubmitTestService(t)
			req := newReplayTestRequest(svc, "probe-sponsor-ok-"+level)
			req.SponsorLevel = level

			if _, err := svc.Submit(ctx, req, "1.2.3.4"); err != nil {
				t.Fatalf("sponsor_level=%q 应被接受: %v", level, err)
			}
		})
	}
}

// TestSubmit_ProofBindsTemplate 锁定收录侧 proof 的模板绑定，以及「模型绑定项在公开流程上
// 恒为空」这条不变量。
//
// 不绑模板就能「用最便宜的 haiku 测通、提交时报成 opus」，收录成的行上线即恒红，而「测通了
// 才准提交」这条闸形同虚设。
//
// 模型不再单独绑：2026-08-21 起公开流程只能选**已声明模型的专属模板**，模型由模板唯一决定，
// 绑住模板即绑住模型（SubmitRequest 根本没有 model 字段）。校验侧因此恒用空串，于是任何
// claims.Model 非空的 proof 在这条路径上都对不上——第二个子测试正是锁住这一点，它同时挡住
// 「拿一份别处签发的、带模型绑定的 proof 来提交」。
func TestSubmit_ProofBindsTemplate(t *testing.T) {
	const apiURL = "https://api.example.com"
	const apiKey = "sk-test-proof-bind-01"

	newReq := func(jobID, template string) *SubmitRequest {
		return &SubmitRequest{
			AgreementAccepted: true,
			ProviderName:      "ProofBindProv",
			WebsiteURL:        "https://example.com",
			Category:          "commercial",
			ServiceType:       "cc",
			TemplateName:      template,
			SponsorLevel:      "pulse",
			ChannelType:       "O",
			ChannelSource:     "nat",
			ChannelGroup:      "main",
			BaseURL:           apiURL,
			APIKey:            apiKey,
			TestProof:         "",
			TestJobID:         jobID,
			TestType:          "cc",
			TestAPIURL:        apiURL,
		}
	}

	t.Run("提交时换模板即拒", func(t *testing.T) {
		svc := newSubmitTestService(t)
		req := newReq("job-tpl-swap", "cc-opus-ping")
		req.TestProof = svc.IssueProof(apikey.ProofClaims{
			JobID: "job-tpl-swap", TestType: "cc", APIURL: apiURL, Variant: "cc-haiku-arith",
		}, apiKey)
		// 同样断言拒因是验签失败：只判 err != nil 会让「被别的前置校验先拒」也算通过
		_, err := svc.Submit(context.Background(), req, "1.2.3.4")
		if err == nil || !strings.Contains(err.Error(), "测试证明无效") {
			t.Fatalf("用 haiku 测通、提交时换成 opus，应验签失败，实际: %v", err)
		}
	})

	t.Run("带模型绑定的 proof 在公开提交侧对不上", func(t *testing.T) {
		svc := newSubmitTestService(t)
		req := newReq("job-model-bound", "cc-glm52-arith")
		req.TestProof = svc.IssueProof(apikey.ProofClaims{
			JobID: "job-model-bound", TestType: "cc", APIURL: apiURL,
			Variant: "cc-glm52-arith", Model: "glm-5.2",
		}, apiKey)
		// 断言拒因必须是验签失败：只判 err != nil 会让「因为别的原因先被拒」也算通过，
		// 而下一个子测试用同一份请求（只是 proof 不带模型）走通了，两者合起来才证明
		// 差异确实来自模型绑定这一项。
		_, err := svc.Submit(context.Background(), req, "1.2.3.4")
		if err == nil || !strings.Contains(err.Error(), "测试证明无效") {
			t.Fatalf("公开提交侧的模型绑定恒为空，带模型的 proof 应验签失败，实际: %v", err)
		}
	})

	t.Run("模板对得上且模型绑定为空即通过", func(t *testing.T) {
		svc := newSubmitTestService(t)
		req := newReq("job-bind-match", "cc-glm52-arith")
		req.TestProof = svc.IssueProof(apikey.ProofClaims{
			JobID: "job-bind-match", TestType: "cc", APIURL: apiURL, Variant: "cc-glm52-arith",
		}, apiKey)
		if _, err := svc.Submit(context.Background(), req, "1.2.3.4"); err != nil {
			t.Fatalf("模板一致、模型绑定为空时应通过: %v", err)
		}
	})
}
