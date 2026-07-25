package change

import (
	"context"
	"errors"
	"testing"

	"monitor/internal/config"
)

// revokedSet 把若干明文 key 转成拒绝名单集合（与 config 侧同一哈希空间）。
func revokedSet(keys ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[config.RevokedKeySHA256Hex(k)] = struct{}{}
	}
	return set
}

// --- AuthIndex 层 ---

// TestAuthIndex_Lookup_RevokedKeyRejected 已泄露 key 即使仍是某通道的在用 key，
// 也必须被判 revoked 且不返回任何候选——否则任何拿到泄露 key 的人都能冒充该通道。
func TestAuthIndex_Lookup_RevokedKeyRejected(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	const leaked = "sk-leaked-key-0001"
	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: leaked},
	}
	idx.Rebuild(monitors, cipher, nil, revokedSet(leaked))

	candidates, revoked := idx.Lookup(leaked, cipher)
	if !revoked {
		t.Fatal("在用但已泄露的 key 必须被判 revoked")
	}
	if candidates != nil {
		t.Fatalf("revoked 时不得返回候选，实际 %d 个", len(candidates))
	}
}

// TestAuthIndex_Lookup_HealthyKeyUnaffected 未在名单里的 key 行为不变。
func TestAuthIndex_Lookup_HealthyKeyUnaffected(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: "sk-healthy-key-01"},
	}
	idx.Rebuild(monitors, cipher, nil, revokedSet("sk-some-other-leak"))

	candidates, revoked := idx.Lookup("sk-healthy-key-01", cipher)
	if revoked {
		t.Fatal("未泄露的 key 不应被判 revoked")
	}
	if len(candidates) != 1 || candidates[0].Provider != "p1" {
		t.Fatalf("候选查找回归: %+v", candidates)
	}
}

// TestAuthIndex_Lookup_RevokedUnknownKey 名单里的 key 即使不在索引里也判 revoked，
// 让调用方能给出「已停用」而不是「查不到通道」的提示。
func TestAuthIndex_Lookup_RevokedUnknownKey(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()
	idx.Rebuild(nil, cipher, nil, revokedSet("sk-leaked-but-removed"))

	if _, revoked := idx.Lookup("sk-leaked-but-removed", cipher); !revoked {
		t.Fatal("名单命中即应判 revoked，与是否在索引内无关")
	}
}

// TestAuthIndex_Rebuild_ReplacesRevokedList 热更新必须整体替换名单（既能加也能减），
// 不能只做并集——否则从名单里移除一条永远不生效。
func TestAuthIndex_Rebuild_ReplacesRevokedList(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	const key = "sk-rotated-key-001"
	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: key},
	}

	idx.Rebuild(monitors, cipher, nil, revokedSet(key))
	if _, revoked := idx.Lookup(key, cipher); !revoked {
		t.Fatal("首轮应判 revoked")
	}

	idx.Rebuild(monitors, cipher, nil, nil)
	candidates, revoked := idx.Lookup(key, cipher)
	if revoked {
		t.Fatal("名单已清空，不应再判 revoked")
	}
	if len(candidates) != 1 {
		t.Fatalf("清空名单后候选应恢复，实际 %d 个", len(candidates))
	}
}

// TestAuthIndex_Rebuild_CopiesRevokedList Rebuild 必须拷贝入参集合，
// 调用方之后改动自己那份不得串改索引内部状态。
func TestAuthIndex_Rebuild_CopiesRevokedList(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	const key = "sk-caller-mutates-1"
	set := revokedSet(key)
	idx.Rebuild(nil, cipher, nil, set)

	delete(set, config.RevokedKeySHA256Hex(key)) // 调用方改自己那份
	if _, revoked := idx.Lookup(key, cipher); !revoked {
		t.Fatal("索引内部名单被外部改动串改了")
	}
}

// TestAuthIndex_RevokedAuthFingerprint 已落库的历史请求只存 HMAC 指纹、不存明文，
// 所以索引要顺带派生「命中名单的在用 key 的 HMAC 指纹」集合，供 admin 侧追溯校验。
func TestAuthIndex_RevokedAuthFingerprint(t *testing.T) {
	cipher := testCipher(t)
	idx := NewAuthIndex()

	const leaked, healthy = "sk-leaked-key-0001", "sk-healthy-key-01"
	monitors := []config.ServiceConfig{
		{Provider: "p1", Service: "cc", Channel: "ch1", APIKey: leaked},
		{Provider: "p2", Service: "cc", Channel: "ch2", APIKey: healthy},
	}
	idx.Rebuild(monitors, cipher, nil, revokedSet(leaked))

	if !idx.IsRevokedAuthFingerprint(cipher.Fingerprint(leaked)) {
		t.Fatal("泄露 key 的 HMAC 指纹应进入撤销指纹集")
	}
	if idx.IsRevokedAuthFingerprint(cipher.Fingerprint(healthy)) {
		t.Fatal("未泄露 key 的指纹不应进入撤销指纹集")
	}
	if idx.IsRevokedAuthFingerprint("") {
		t.Fatal("空指纹不得命中")
	}
}

// --- Service 层 ---

// serviceWithRevoked 构造一个把 testprov 的在用 key 标为已泄露的 Service。
func serviceWithRevoked(t *testing.T) (*Service, *mockStore, string) {
	t.Helper()
	svc, store := newTestService(t)
	const leaked = "sk-test-key-12345" // newTestService 里 testprov/cc/vip 的在用 key

	cfg := &config.ChangeRequestConfig{
		Enabled:          true,
		MaxPerIPPerDay:   5,
		RevokedKeySHA256: revokedSet(leaked),
	}
	svc.UpdateConfig(cfg, []config.ServiceConfig{
		{
			Provider: "testprov", Service: "cc", Channel: "vip",
			APIKey:       leaked,
			ProviderName: "TestProvider", ChannelName: "VIP Channel",
			Category: "commercial", BaseURL: "https://api.test.com",
		},
		{
			Provider: "other", Service: "cc", Channel: "free",
			APIKey:  "sk-other-key-6789",
			BaseURL: "https://api.other.com",
		},
	})
	return svc, store, leaked
}

// TestService_Auth_RevokedKey 认证端返回可区分的 typed error，让前端能提示"请联系我们更换"，
// 而不是复用"查不到通道"的通用文案。
func TestService_Auth_RevokedKey(t *testing.T) {
	svc, _, leaked := serviceWithRevoked(t)

	_, err := svc.Auth(leaked)
	if err == nil {
		t.Fatal("已泄露 key 必须认证失败")
	}
	var revokedErr *RevokedAPIKeyError
	if !errors.As(err, &revokedErr) {
		t.Fatalf("应返回 RevokedAPIKeyError，实际 %T: %v", err, err)
	}
}

// TestService_Auth_HealthyKeyStillWorks 同一份配置下未泄露的 key 不受影响。
func TestService_Auth_HealthyKeyStillWorks(t *testing.T) {
	svc, _, _ := serviceWithRevoked(t)

	resp, err := svc.Auth("sk-other-key-6789")
	if err != nil {
		t.Fatalf("未泄露 key 认证不应失败: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("候选数 = %d, want 1", len(resp.Candidates))
	}
}

// TestService_Submit_RevokedKey Submit 是第二个认证入口，必须同样拦下；
// 且要早于字段校验触发（不能让泄露 key 的请求先跑一遍业务校验）。
func TestService_Submit_RevokedKey(t *testing.T) {
	svc, _, leaked := serviceWithRevoked(t)

	_, err := svc.Submit(context.Background(), &SubmitRequest{
		APIKey:          leaked,
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "改名"},
	}, "127.0.0.1")
	if err == nil {
		t.Fatal("已泄露 key 提交必须失败")
	}
	var revokedErr *RevokedAPIKeyError
	if !errors.As(err, &revokedErr) {
		t.Fatalf("应返回 RevokedAPIKeyError，实际 %T: %v", err, err)
	}
}

// TestService_Submit_RevokedNewAPIKey 轮换后的新 key 不能又是一把已泄露的 key，
// 否则"换 key"等于原地踏步。
func TestService_Submit_RevokedNewAPIKey(t *testing.T) {
	svc, store := newTestService(t)
	const leaked = "sk-leaked-elsewhere"

	cfg := &config.ChangeRequestConfig{
		Enabled:          true,
		MaxPerIPPerDay:   5,
		RevokedKeySHA256: revokedSet(leaked),
	}
	svc.UpdateConfig(cfg, []config.ServiceConfig{
		{
			Provider: "testprov", Service: "cc", Channel: "vip",
			APIKey: "sk-test-key-12345", BaseURL: "https://api.test.com",
		},
	})
	_ = store

	_, err := svc.Submit(context.Background(), &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{},
		NewAPIKey:         leaked,
		AgreementAccepted: true,
	}, "127.0.0.1")
	if err == nil {
		t.Fatal("新 key 命中泄露名单必须拒绝")
	}
	var revokedErr *RevokedAPIKeyError
	if !errors.As(err, &revokedErr) {
		t.Fatalf("应返回 RevokedAPIKeyError，实际 %T: %v", err, err)
	}
}

// TestService_AdminApprove_RevokedHistoricalRequest 名单生效前已落库的请求不会被 Submit 闸拦到，
// 管理员批准时必须补上同一道闸——否则撤销对历史队列不追溯，攻击者在窗口期塞进来的 payload
// 仍可能被批准应用。（与 v2.69.0 反作弊 admin 闸同一类后门。）
func TestService_AdminApprove_RevokedHistoricalRequest(t *testing.T) {
	svc, store, leaked := serviceWithRevoked(t)
	ctx := context.Background()

	cr := makeRequest("pub-revoked")
	cr.AuthFingerprint = svc.cipher.Fingerprint(leaked)
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminApprove(ctx, "pub-revoked", "ok"); err == nil {
		t.Fatal("认证指纹已撤销的历史请求不得被批准")
	}

	got, _ := store.GetByPublicID(ctx, "pub-revoked")
	if got.Status != StatusPending {
		t.Fatalf("被拒后状态应保持 pending，实际 %q", got.Status)
	}
}

// TestService_AdminApprove_HealthyRequestUnaffected 未撤销指纹的历史请求照常可批准。
func TestService_AdminApprove_HealthyRequestUnaffected(t *testing.T) {
	svc, store, _ := serviceWithRevoked(t)
	ctx := context.Background()

	cr := makeRequest("pub-ok")
	cr.AuthFingerprint = svc.cipher.Fingerprint("sk-other-key-6789")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminApprove(ctx, "pub-ok", "ok"); err != nil {
		t.Fatalf("未撤销请求应可批准: %v", err)
	}
}

// TestService_AdminApply_RevokedHistoricalRequest Apply 是另一条落地路径，同样要拦。
func TestService_AdminApply_RevokedHistoricalRequest(t *testing.T) {
	svc, store, leaked := serviceWithRevoked(t)
	ctx := context.Background()

	svc.SetMonitorStore(config.NewMonitorStore(t.TempDir()))

	cr := makeRequest("pub-revoked-apply")
	cr.AuthFingerprint = svc.cipher.Fingerprint(leaked)
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 必须断言是**撤销**这条原因失败，否则空 monitors.d 造成的其它失败也会让测试假绿。
	err := svc.AdminApply(ctx, "pub-revoked-apply")
	if err == nil {
		t.Fatal("认证指纹已撤销的历史请求不得被应用")
	}
	var revokedErr *RevokedAPIKeyError
	if !errors.As(err, &revokedErr) {
		t.Fatalf("应返回 RevokedAPIKeyError，实际 %T: %v", err, err)
	}
}
