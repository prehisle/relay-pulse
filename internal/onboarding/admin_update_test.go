package onboarding

import (
	"context"
	"errors"
	"testing"
	"time"

	"monitor/internal/config"
)

// newTestService 基于内存 SQLite store 构造一个最小可用的 Service（仅覆盖 AdminUpdate 所需依赖）。
func newTestService(t *testing.T) (*Service, *SQLStore) {
	t.Helper()
	store := newTestStore(t)
	cfg := &config.OnboardingConfig{
		EncryptionKey:    testKey(),
		ProofSecret:      "test-proof-secret",
		ProofTTLDuration: 5 * time.Minute,
	}
	svc, err := NewService(store, cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store
}

// TestAdminUpdate_RederiveGuard 锁定 AdminUpdate 的四元组重派生护栏行为：
// 仅当 service/type/source/group 任一变化时才重新校验并重派生 channel_code，
// 否则保留库内原值（保护 legacy 两段记录不被无关编辑改写成三段）。
func TestAdminUpdate_RederiveGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("无关字段编辑不触发重派生", func(t *testing.T) {
		svc, store := newTestService(t)
		// legacy 两段记录：source=api、group 为空、channel_code 存为两段。
		saveSubmission(t, store, "legacy-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "legacy-1", map[string]any{
			"admin_note": "仅改备注",
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ChannelCode != "o-api" {
			t.Errorf("无关字段编辑应保留两段 channel_code o-api，实际 %q", got.ChannelCode)
		}
		if got.ChannelGroup != "" {
			t.Errorf("无关字段编辑不应补全 channel_group，实际 %q", got.ChannelGroup)
		}
		if got.AdminNote != "仅改备注" {
			t.Errorf("admin_note 未写入，实际 %q", got.AdminNote)
		}
	})

	t.Run("改 channel_group 触发重派生为三段", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "regroup-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "regroup-1", map[string]any{
			"channel_group": "US", // 大写应被归一化为小写
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ChannelCode != "o-api-us" {
			t.Errorf("改 group 应重派生为 o-api-us，实际 %q", got.ChannelCode)
		}
		if got.ChannelGroup != "us" {
			t.Errorf("channel_group 应归一化为 us，实际 %q", got.ChannelGroup)
		}
	})

	t.Run("改 channel_source 触发重派生且空 group 回退 main", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "resrc-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "resrc-1", map[string]any{
			"channel_source": "MAX", // cc 词表含 max；大写应归一化
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ChannelCode != "o-max-main" {
			t.Errorf("改 source 应重派生为 o-max-main（空 group 回退 main），实际 %q", got.ChannelCode)
		}
		if got.ChannelSource != "max" || got.ChannelGroup != "main" {
			t.Errorf("source/group 归一化错误：source=%q group=%q", got.ChannelSource, got.ChannelGroup)
		}
	})

	t.Run("非法 channel_source 被词表校验拒绝", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "badsrc-1", "pending", 100)

		if _, err := svc.AdminUpdate(ctx, "badsrc-1", map[string]any{
			"channel_source": "zzz", // 任何 service 词表都不含
		}); err == nil {
			t.Errorf("非法 channel_source zzz 应被拒绝")
		}
	})

	t.Run("非法枚举 channel_type / service_type 被拒绝", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "badenum-1", "pending", 100)

		if _, err := svc.AdminUpdate(ctx, "badenum-1", map[string]any{
			"channel_type": "X",
		}); err == nil {
			t.Errorf("非法 channel_type X 应被拒绝")
		}
		if _, err := svc.AdminUpdate(ctx, "badenum-1", map[string]any{
			"service_type": "zz",
		}); err == nil {
			t.Errorf("非法 service_type zz 应被拒绝")
		}
	})

	t.Run("改 service_type 后 source 须在新 service 词表内", func(t *testing.T) {
		svc, store := newTestService(t)
		// source=api：cc 与 cx 词表均含 api，所以仅切 service_type 到 cx 仍合法且重派生。
		saveSubmission(t, store, "svc-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "svc-1", map[string]any{
			"service_type": "cx",
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ServiceType != "cx" || got.ChannelCode != "o-api-main" {
			t.Errorf("切 service_type 到 cx 应重派生为 o-api-main，实际 service=%q code=%q", got.ServiceType, got.ChannelCode)
		}
	})

	t.Run("仅改 channel_type 与现有 source 类别不符被拒", func(t *testing.T) {
		svc, store := newTestService(t)
		// 基础记录 type=O、source=api(official)。改 type=R（仅允许 reverse）应被自洽校验拒绝。
		saveSubmission(t, store, "tcm-1", "pending", 100)

		if _, err := svc.AdminUpdate(ctx, "tcm-1", map[string]any{
			"channel_type": "R",
		}); err == nil {
			t.Errorf("O→R 但 source=api 仍为官方类，应被类型↔来源自洽校验拒绝")
		}
	})

	t.Run("同时改 channel_type+source 到自洽组合通过并重派生", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "tcm-2", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "tcm-2", map[string]any{
			"channel_type":   "R",
			"channel_source": "kiro", // cc 词表含 kiro(reverse)，与 R 自洽
		})
		if err != nil {
			t.Fatalf("R+kiro 应通过: %v", err)
		}
		if got.ChannelCode != "r-kiro-main" {
			t.Errorf("R+kiro 应重派生为 r-kiro-main（空 group 回退 main），实际 %q", got.ChannelCode)
		}
	})

	t.Run("channel_name 过校验：中文放行、不可见字符拒绝", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "chname-1", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "chname-1", map[string]any{
			"channel_name": "  华东线路 ",
		})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.ChannelName != "华东线路" {
			t.Errorf("channel_name 应剪除首尾空白后写入，实际 %q", got.ChannelName)
		}
		if _, err := svc.AdminUpdate(ctx, "chname-1", map[string]any{
			"channel_name": "a\u202eb", // bidi 方向控制符
		}); err == nil {
			t.Errorf("含 bidi 控制符的 channel_name 应被拒绝")
		}
	})
}

// TestAdminUpdate_PSCOverrideValidation 锁定「PSC 覆盖值在保存期就 fail-fast」这条契约。
// 此前三个 target_* 原样入库、直到上架才报错，管理员会看到「保存成功 → 上架失败」的错位反馈；
// 而它们最终是 monitors.d/ 的文件名分段与公开 URL slug，坏值根本不该落库。
func TestAdminUpdate_PSCOverrideValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("中文覆盖值被拒且不落库", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "ovr-cn", "pending", 100)

		_, err := svc.AdminUpdate(ctx, "ovr-cn", map[string]any{"target_provider": "银兔"})
		if err == nil {
			t.Fatal("中文 target_provider 应被拒绝")
		}
		var overrideErr *InvalidPSCOverrideError
		if !errors.As(err, &overrideErr) {
			t.Fatalf("期望 *InvalidPSCOverrideError，实际 %T: %v", err, err)
		}

		sub, err := store.GetByPublicID(ctx, "ovr-cn")
		if err != nil {
			t.Fatalf("GetByPublicID: %v", err)
		}
		if sub.TargetProvider != "" {
			t.Errorf("被拒的覆盖值不应落库，实际 %q", sub.TargetProvider)
		}
	})

	t.Run("连续短横线覆盖值被拒", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "ovr-hyphen", "pending", 100)

		_, err := svc.AdminUpdate(ctx, "ovr-hyphen", map[string]any{
			"target_provider": "sai--ai",
		})
		if err == nil {
			t.Fatal("连续短横线覆盖值应被拒绝（会让 monitors.d/ 文件名分段解析错位 + 热加载失败）")
		}
		var overrideErr *InvalidPSCOverrideError
		if !errors.As(err, &overrideErr) {
			t.Fatalf("期望 *InvalidPSCOverrideError，实际 %T: %v", err, err)
		}
		if overrideErr.Field != "target_provider" {
			t.Errorf("Field = %q，期望 target_provider", overrideErr.Field)
		}
	})

	t.Run("合法覆盖值写入并剪除首尾空白", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "ovr-ok", "pending", 100)

		got, err := svc.AdminUpdate(ctx, "ovr-ok", map[string]any{"target_provider": "  yintu "})
		if err != nil {
			t.Fatalf("AdminUpdate: %v", err)
		}
		if got.TargetProvider != "yintu" {
			t.Errorf("TargetProvider = %q，期望 yintu（已剪空白）", got.TargetProvider)
		}
	})

	// 空串是合法输入：表示清空覆盖、回落到从展示名派生。校验若带 `!= ""` 短路以外的写法把空串
	// 也拦掉，管理员就再也删不掉一个填错的代号了。
	t.Run("空串可清空覆盖值", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "ovr-clear", "pending", 100)

		if _, err := svc.AdminUpdate(ctx, "ovr-clear", map[string]any{"target_provider": "yintu"}); err != nil {
			t.Fatalf("AdminUpdate(set): %v", err)
		}
		got, err := svc.AdminUpdate(ctx, "ovr-clear", map[string]any{"target_provider": ""})
		if err != nil {
			t.Fatalf("AdminUpdate(clear): %v", err)
		}
		if got.TargetProvider != "" {
			t.Errorf("空串应清空覆盖值，实际 %q", got.TargetProvider)
		}
	})

	// 存量脏数据不该被无关编辑卡死：只校验本次请求真正带来的字段。
	t.Run("库内已有脏覆盖值时编辑无关字段仍放行", func(t *testing.T) {
		svc, store := newTestService(t)
		saveSubmission(t, store, "ovr-legacy", "pending", 100)
		sub, err := store.GetByPublicID(ctx, "ovr-legacy")
		if err != nil {
			t.Fatalf("GetByPublicID: %v", err)
		}
		sub.TargetProvider = "银兔" // 模拟本闸上线前存进去的脏值
		if err := store.Update(ctx, sub); err != nil {
			t.Fatalf("store.Update: %v", err)
		}

		got, err := svc.AdminUpdate(ctx, "ovr-legacy", map[string]any{"admin_note": "只改备注"})
		if err != nil {
			t.Fatalf("无关字段编辑不应被存量脏数据卡住，实际 %v", err)
		}
		// 断言真落库：只测「没报错」的话，实现改成「有脏值就直接 return，什么也不写」也照样绿。
		persisted, err := store.GetByPublicID(ctx, "ovr-legacy")
		if err != nil {
			t.Fatalf("GetByPublicID: %v", err)
		}
		if got.AdminNote != "只改备注" || persisted.AdminNote != "只改备注" {
			t.Errorf("admin_note 未落库：返回 %q，库内 %q", got.AdminNote, persisted.AdminNote)
		}
		if persisted.TargetProvider != "银兔" {
			t.Errorf("无关编辑不应改动存量脏值，实际 %q", persisted.TargetProvider)
		}
	})
}
