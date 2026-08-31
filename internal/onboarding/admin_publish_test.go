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

	// 管理员填了非法覆盖值：与「没填覆盖值」是两类不同处置（那条让你去填，这条让你改这一格），
	// 故走独立的 InvalidPSCOverrideError、由 handler 映射 400。此前这条落进通用校验被报成 500，
	// 管理员看到的是一个像服务端故障的错误、却说不出该改哪里。错误里必须带得出字段名与原值，
	// 否则前端无法把提示落到具体输入框上。
	t.Run("非法target_provider覆盖值返回InvalidPSCOverrideError且不写盘", func(t *testing.T) {
		svc, store, monitorStore := newPublishTestService(t)
		savePublishableSubmission(t, svc, store, "pub-cn-bad-override", "赛博AI", "还是中文")

		err := svc.AdminPublish(ctx, "pub-cn-bad-override", "hot")
		if err == nil {
			t.Fatal("期望 AdminPublish 返回错误，实际 nil")
		}

		var overrideErr *InvalidPSCOverrideError
		if !errors.As(err, &overrideErr) {
			t.Fatalf("期望错误类型 *InvalidPSCOverrideError，实际 %T: %v", err, err)
		}
		if overrideErr.Field != "target_provider" {
			t.Errorf("Field = %q，期望 target_provider", overrideErr.Field)
		}
		if overrideErr.Value != "还是中文" {
			t.Errorf("Value = %q，期望 还是中文", overrideErr.Value)
		}
		// 消息要能自解释：既指出是哪一格，也说清合法格式。
		if !strings.Contains(err.Error(), "Provider 覆盖") {
			t.Errorf("错误消息应点名「Provider 覆盖」输入框，实际 %v", err)
		}

		var slugErr *InvalidProviderSlugError
		if errors.As(err, &slugErr) {
			t.Fatalf("填了覆盖值就不该再报「派生失败、请去填覆盖值」，实际 %v", err)
		}

		summaries, err := monitorStore.List()
		if err != nil {
			t.Fatalf("monitorStore.List: %v", err)
		}
		if len(summaries) != 0 {
			t.Errorf("monitors.d/ 应为空，实际写入 %d 个文件: %+v", len(summaries), summaries)
		}
	})

	// -17b 的覆盖路径版本：`sai--ai` 这类连续短横线值过得了历史的宽松 PSC 正则，却过不了 loader 的
	// ValidateProviderSlug，且段内的 `--` 还会让 monitors.d/ 文件名分段解析错位。放行 = 上架返回
	// 200、热加载整份配置失败、重启拉不起来，是本轮最该堵死的形状。
	t.Run("连续短横线target_provider覆盖值应被拒且不写盘", func(t *testing.T) {
		svc, store, monitorStore := newPublishTestService(t)
		savePublishableSubmission(t, svc, store, "pub-double-hyphen-override", "SaiAI", "sai--ai")

		err := svc.AdminPublish(ctx, "pub-double-hyphen-override", "hot")
		if err == nil {
			t.Fatal("期望 AdminPublish 拒绝连续短横线覆盖值，实际 nil（会造出热加载失败的坏文件）")
		}
		var overrideErr *InvalidPSCOverrideError
		if !errors.As(err, &overrideErr) {
			t.Fatalf("期望错误类型 *InvalidPSCOverrideError，实际 %T: %v", err, err)
		}

		summaries, err := monitorStore.List()
		if err != nil {
			t.Fatalf("monitorStore.List: %v", err)
		}
		if len(summaries) != 0 {
			t.Errorf("monitors.d/ 应为空，实际写入 %d 个文件: %+v", len(summaries), summaries)
		}
	})

	// 对称锁定：service/channel 两个覆盖值走同一条分流，别只修 provider 一格。
	t.Run("非法target_service与target_channel覆盖值同样返回InvalidPSCOverrideError", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			publicID string
			field    string
			apply    func(*Submission)
		}{
			{"target_service", "pub-bad-service", "target_service", func(s *Submission) { s.TargetService = "CC" }},
			{"target_channel", "pub-bad-channel", "target_channel", func(s *Submission) { s.TargetChannel = "o-max-" }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc, store, monitorStore := newPublishTestService(t)
				sub := savePublishableSubmission(t, svc, store, tc.publicID, "SaiAI", "")
				tc.apply(sub)
				if err := store.Update(ctx, sub); err != nil {
					t.Fatalf("store.Update: %v", err)
				}

				err := svc.AdminPublish(ctx, tc.publicID, "hot")
				var overrideErr *InvalidPSCOverrideError
				if !errors.As(err, &overrideErr) {
					t.Fatalf("期望错误类型 *InvalidPSCOverrideError，实际 %T: %v", err, err)
				}
				if overrideErr.Field != tc.field {
					t.Errorf("Field = %q，期望 %s", overrideErr.Field, tc.field)
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
