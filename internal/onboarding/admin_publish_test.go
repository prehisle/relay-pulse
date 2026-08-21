package onboarding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"monitor/internal/config"
)

// newPublishTestService 在 newTestService（admin_update_test.go）共用的 store/cfg 构造之上，
// 补齐 AdminPublish 特有的两个硬依赖：configDir 下的 templates/ 目录（validateMonitorConfig 发布前
// 校验模板文件存在）与已接线的 MonitorStore（发布写入 monitors.d/）。二者与
// internal/api/monitor_handler_test.go::newAdminMonitorTestHandler、
// internal/config/monitor_store_test.go 系列一致的 `t.TempDir()+MonitorsDirName` 惯用法同源，
// 不是另起的 mock 框架。
func newPublishTestService(t *testing.T) (*Service, *SQLStore, *config.MonitorStore) {
	t.Helper()

	store := newTestStore(t)
	cfg := &config.OnboardingConfig{
		EncryptionKey:    testKey(),
		ProofSecret:      "test-proof-secret",
		ProofTTLDuration: 5 * time.Minute,
	}

	configDir := t.TempDir()
	templatesDir := filepath.Join(configDir, "templates")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(templates): %v", err)
	}
	// 最小合法探测模板：LoadProbeTemplate 仅硬性要求 method 非空。
	// 声明 model：validateMonitorConfig 会拒绝「模板与监测行都没有模型」的待发布配置
	// （那种行上线即在 wire 上发 `"model": ""`）。真实模板除 native 族外都声明模型。
	const tplJSON = `{"method":"GET","url":"{{BASE_URL}}","model":"Haiku","request_model":"claude-haiku-4-5"}`
	if err := os.WriteFile(filepath.Join(templatesDir, "tpl.json"), []byte(tplJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(tpl.json): %v", err)
	}

	svc, err := NewService(store, cfg, configDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	monitorsDir := filepath.Join(configDir, config.MonitorsDirName)
	if err := os.MkdirAll(monitorsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(monitors.d): %v", err)
	}
	monitorStore := config.NewMonitorStore(monitorsDir)
	svc.SetMonitorStore(monitorStore)

	return svc, store, monitorStore
}

// savePublishableSubmission 落库一条状态为 pending、可被 AdminPublish 处理的申请，
// providerName/targetProvider 由调用方指定以覆盖 TestInvalidProviderSlug 各子用例（未覆盖派生非法/
// 覆盖为合法英文代号/非法覆盖值落回通用校验）。与 saveSubmission（store_sql_test.go）同构，
// 但暴露这两个字段供本文件场景定制。
func savePublishableSubmission(t *testing.T, svc *Service, store *SQLStore, publicID, providerName, targetProvider string) *Submission {
	t.Helper()

	const rawAPIKey = "sk-test-0123456789"
	encrypted, err := svc.cipher.Encrypt(rawAPIKey)
	if err != nil {
		t.Fatalf("cipher.Encrypt: %v", err)
	}
	fingerprint := svc.cipher.Fingerprint(rawAPIKey)

	now := time.Now().Unix()
	sub := &Submission{
		PublicID:          publicID,
		Status:            StatusPending,
		ProviderName:      providerName,
		WebsiteURL:        "https://example.com",
		Category:          "commercial",
		ServiceType:       "cc",
		TemplateName:      "tpl",
		SponsorLevel:      "pulse",
		ChannelType:       "O",
		ChannelSource:     "api",
		ChannelGroup:      "main",
		ChannelCode:       "o-api-main",
		TargetProvider:    targetProvider,
		BaseURL:           "https://api.example.com",
		APIKeyEncrypted:   encrypted,
		APIKeyFingerprint: fingerprint,
		APIKeyLast4:       Last4(rawAPIKey),
		TestJobID:         "job-" + publicID,
		TestPassedAt:      now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := store.Save(context.Background(), sub); err != nil {
		t.Fatalf("store.Save(%s): %v", publicID, err)
	}
	return sub
}

// TestAdminPublish_InvalidProviderSlug 锁定「中文服务商名未补 target_provider 时发布返回可操作
// 4xx」这条契约：Task1 放开 validateProviderName 允许中文展示名后，BuildServiceConfigFromSubmission
// 派生的机器 slug（ToLower+空格转短横线）对中文名必然非法，AdminPublish 须在写盘前拦下并返回
// InvalidProviderSlugError（而非落到 validateMonitorConfig 的通用错误、在 handler 层呈现成 500）。
func TestAdminPublish_InvalidProviderSlug(t *testing.T) {
	ctx := context.Background()

	t.Run("中文展示名未覆盖target_provider应返回InvalidProviderSlugError且不写盘不发布", func(t *testing.T) {
		svc, store, monitorStore := newPublishTestService(t)
		savePublishableSubmission(t, svc, store, "pub-cn-no-override", "赛博AI", "")

		err := svc.AdminPublish(ctx, "pub-cn-no-override", "hot")
		if err == nil {
			t.Fatal("期望 AdminPublish 返回错误，实际 nil")
		}

		var slugErr *InvalidProviderSlugError
		if !errors.As(err, &slugErr) {
			t.Fatalf("期望错误类型 *InvalidProviderSlugError，实际 %T: %v", err, err)
		}
		if slugErr.ProviderName != "赛博AI" {
			t.Errorf("ProviderName = %q，期望 赛博AI", slugErr.ProviderName)
		}
		if slugErr.DerivedSlug == "" {
			t.Errorf("DerivedSlug 不应为空")
		}

		// 不应写入 monitors.d/。
		summaries, err := monitorStore.List()
		if err != nil {
			t.Fatalf("monitorStore.List: %v", err)
		}
		if len(summaries) != 0 {
			t.Errorf("monitors.d/ 应为空，实际写入 %d 个文件: %+v", len(summaries), summaries)
		}

		// 申请状态不应变为 published。
		sub, err := store.GetByPublicID(ctx, "pub-cn-no-override")
		if err != nil {
			t.Fatalf("GetByPublicID: %v", err)
		}
		if sub.Status == StatusPublished {
			t.Errorf("申请状态不应变为 published，实际 %q", sub.Status)
		}
	})

	t.Run("中文展示名+target_provider覆盖英文代号应成功发布且展示名保留", func(t *testing.T) {
		svc, store, monitorStore := newPublishTestService(t)
		savePublishableSubmission(t, svc, store, "pub-cn-override", "赛博AI", "saiai")

		if err := svc.AdminPublish(ctx, "pub-cn-override", "hot"); err != nil {
			t.Fatalf("AdminPublish: %v", err)
		}

		key := config.MonitorFileKeyFromPSC("saiai", "cc", "o-api-main")
		file, err := monitorStore.Get(key)
		if err != nil {
			t.Fatalf("monitorStore.Get(%s): %v", key, err)
		}
		if file == nil || len(file.Monitors) != 1 {
			t.Fatalf("期望写入单条 monitor，实际 %+v", file)
		}
		got := file.Monitors[0]
		if got.Provider != "saiai" {
			t.Errorf("Provider = %q，期望 saiai（应取 target_provider 覆盖值）", got.Provider)
		}
		if got.ProviderName != "赛博AI" {
			t.Errorf("ProviderName = %q，期望 赛博AI（展示名应原样保留）", got.ProviderName)
		}

		sub, err := store.GetByPublicID(ctx, "pub-cn-override")
		if err != nil {
			t.Fatalf("GetByPublicID: %v", err)
		}
		if sub.Status != StatusPublished {
			t.Errorf("申请状态应变为 published，实际 %q", sub.Status)
		}
	})

	// 边界锁定：管理员填了非法 target_provider 覆盖值（如又填了中文）属**本轮范围外**的另一类
	// 问题——它与非法 target_service/target_channel 覆盖对称，同属预存的通用校验缺口，不由本轮的
	// InvalidProviderSlugError 承接。护栏刻意只在 target_provider 为空（即走展示名自动派生路径）时
	// 触发；填了覆盖值就交给下方 validateMonitorConfig 通用校验（当前呈现为 500，是既有对称技术债）。
	// 本用例守住这条边界：非法覆盖值**不**返回 InvalidProviderSlugError，但仍返回非 nil 错误且不写盘。
	t.Run("非法target_provider覆盖值落回通用校验错误_不由InvalidProviderSlugError承接", func(t *testing.T) {
		svc, store, monitorStore := newPublishTestService(t)
		savePublishableSubmission(t, svc, store, "pub-cn-bad-override", "赛博AI", "还是中文")

		err := svc.AdminPublish(ctx, "pub-cn-bad-override", "hot")
		if err == nil {
			t.Fatal("期望 AdminPublish 返回错误，实际 nil")
		}

		var slugErr *InvalidProviderSlugError
		if errors.As(err, &slugErr) {
			t.Fatalf("非法 target_provider 覆盖值属范围外，不应返回 *InvalidProviderSlugError，实际却返回了：%v", err)
		}

		// 进一步锁定"落回通用校验"这条路径：错误须来自 validatePSCSegment（其消息含稳定子串
		// "格式无效"，经 validateMonitorConfig→AdminPublish 包装为「待发布 monitor 配置无效: ...」），
		// 而非任何更早的异常（解密失败/申请不存在/状态非法等）。这样即便将来护栏或错误分流被改动，
		// 也能 fail-loud 提示边界移位。
		if !strings.Contains(err.Error(), "格式无效") {
			t.Errorf("期望错误来自通用 PSC 校验（消息含「格式无效」），实际 %v", err)
		}

		summaries, err := monitorStore.List()
		if err != nil {
			t.Fatalf("monitorStore.List: %v", err)
		}
		if len(summaries) != 0 {
			t.Errorf("monitors.d/ 应为空，实际写入 %d 个文件: %+v", len(summaries), summaries)
		}
	})

	// 非回归锁定：ASCII 展示名未填 target_provider 时，护栏必须放行、正常发布——新护栏只该拦
	// 「派生 slug 非法」，绝不能误伤 ASCII 名的自动派生 happy path（slug=lower(展示名)）。
	t.Run("ASCII展示名未覆盖target_provider应正常发布_派生小写slug且展示名保留", func(t *testing.T) {
		svc, store, monitorStore := newPublishTestService(t)
		savePublishableSubmission(t, svc, store, "pub-ascii-no-override", "SaiAI", "")

		if err := svc.AdminPublish(ctx, "pub-ascii-no-override", "hot"); err != nil {
			t.Fatalf("ASCII 名应正常发布，实际 AdminPublish 返回错误：%v", err)
		}

		key := config.MonitorFileKeyFromPSC("saiai", "cc", "o-api-main")
		file, err := monitorStore.Get(key)
		if err != nil {
			t.Fatalf("monitorStore.Get(%s): %v", key, err)
		}
		if file == nil || len(file.Monitors) != 1 {
			t.Fatalf("期望写入单条 monitor，实际 %+v", file)
		}
		got := file.Monitors[0]
		if got.Provider != "saiai" {
			t.Errorf("Provider = %q，期望 saiai（ASCII 展示名 lower 派生的 slug）", got.Provider)
		}
		if got.ProviderName != "SaiAI" {
			t.Errorf("ProviderName = %q，期望 SaiAI（展示名应原样保留大小写）", got.ProviderName)
		}

		sub, err := store.GetByPublicID(ctx, "pub-ascii-no-override")
		if err != nil {
			t.Fatalf("GetByPublicID: %v", err)
		}
		if sub.Status != StatusPublished {
			t.Errorf("申请状态应变为 published，实际 %q", sub.Status)
		}
	})

	// -17b 回归锁定：ASCII 展示名派生出**非法 slug**（连续短横线，如「Sai  AI」双空格 → sai--ai）时，
	// 未覆盖 target_provider 也应被守卫在写盘前拦下。此前守卫用宽松的 pscSegmentPattern（允许连续 --），
	// sai--ai 会过发布校验写盘、却在 loader 的 ValidateProviderSlug（禁连续 --）热加载时被拒
	// （「写盘成功、热加载失败」）。守卫改用 config.ValidateProviderSlug（与 loader 同一函数）后 fail-closed。
	t.Run("ASCII双空格名派生连续短横线slug未覆盖应返回InvalidProviderSlugError且不写盘", func(t *testing.T) {
		svc, store, monitorStore := newPublishTestService(t)
		savePublishableSubmission(t, svc, store, "pub-ascii-double-space", "Sai  AI", "")

		err := svc.AdminPublish(ctx, "pub-ascii-double-space", "hot")
		if err == nil {
			t.Fatal("期望 AdminPublish 返回错误，实际 nil")
		}
		var slugErr *InvalidProviderSlugError
		if !errors.As(err, &slugErr) {
			t.Fatalf("期望 *InvalidProviderSlugError（sai--ai 连续短横线非法），实际 %T: %v", err, err)
		}
		if slugErr.DerivedSlug != "sai--ai" {
			t.Errorf("DerivedSlug = %q，期望 sai--ai", slugErr.DerivedSlug)
		}

		summaries, err := monitorStore.List()
		if err != nil {
			t.Fatalf("monitorStore.List: %v", err)
		}
		if len(summaries) != 0 {
			t.Errorf("monitors.d/ 应为空，实际写入 %d 个文件: %+v", len(summaries), summaries)
		}
	})
}
