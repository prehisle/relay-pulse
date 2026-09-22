package api

import (
	"math"
	"testing"
	"time"

	"monitor/internal/automove"
	"monitor/internal/config"
	"monitor/internal/storage"
)

// TestBuildTimelineLatencyCalculation 测试延迟统计逻辑
// 验证：
// 1. 优先使用 status > 0 的记录计算平均延迟
// 2. 若全部不可用，则使用所有记录的平均延迟作为参考（前端显示灰色）
func TestBuildTimelineLatencyCalculation(t *testing.T) {
	// 创建 Handler（不需要真实的 storage）
	h := &Handler{
		config: &config.AppConfig{
			DegradedWeight: 0.7,
		},
	}

	now := time.Now()

	tests := []struct {
		name            string
		records         []*storage.ProbeRecord
		expectedLatency int // 预期的平均延迟
		description     string
	}{
		{
			name: "全部可用状态",
			records: []*storage.ProbeRecord{
				{Status: 1, Latency: 100, Timestamp: now.Unix()},
				{Status: 1, Latency: 200, Timestamp: now.Unix()},
				{Status: 1, Latency: 300, Timestamp: now.Unix()},
			},
			expectedLatency: 200, // (100+200+300)/3 = 200
			description:     "3条绿色记录，平均延迟应为200ms",
		},
		{
			name: "混合可用和不可用状态",
			records: []*storage.ProbeRecord{
				{Status: 1, Latency: 100, Timestamp: now.Unix()},  // 可用，纳入统计
				{Status: 0, Latency: 5000, Timestamp: now.Unix()}, // 不可用，不纳入统计
				{Status: 1, Latency: 200, Timestamp: now.Unix()},  // 可用，纳入统计
			},
			expectedLatency: 150, // (100+200)/2 = 150，排除 status=0 的 5000ms
			description:     "2条绿色+1条红色，红色的5000ms不应纳入统计",
		},
		{
			name: "混合绿色和黄色状态",
			records: []*storage.ProbeRecord{
				{Status: 1, Latency: 100, Timestamp: now.Unix()}, // 绿色
				{Status: 2, Latency: 300, Timestamp: now.Unix()}, // 黄色（慢）
				{Status: 1, Latency: 200, Timestamp: now.Unix()}, // 绿色
			},
			expectedLatency: 200, // (100+300+200)/3 = 200，黄色也应纳入统计
			description:     "绿色和黄色都应纳入延迟统计",
		},
		{
			name: "全部不可用状态",
			records: []*storage.ProbeRecord{
				{Status: 0, Latency: 1000, Timestamp: now.Unix()},
				{Status: 0, Latency: 2000, Timestamp: now.Unix()},
			},
			expectedLatency: 1500, // 全部不可用时，返回所有记录平均延迟作为参考 (1000+2000)/2
			description:     "全部不可用时，返回所有记录平均延迟作为参考值",
		},
		{
			name: "全部不可用且延迟为0",
			records: []*storage.ProbeRecord{
				{Status: 0, Latency: 0, Timestamp: now.Unix()}, // 网络错误，无延迟数据
				{Status: 0, Latency: 0, Timestamp: now.Unix()},
			},
			expectedLatency: 0, // 没有有效延迟数据，返回 0
			description:     "全部不可用且无延迟数据时，延迟为0",
		},
		{
			name: "混合所有状态",
			records: []*storage.ProbeRecord{
				{Status: 1, Latency: 100, Timestamp: now.Unix()},  // 绿色，纳入
				{Status: 2, Latency: 200, Timestamp: now.Unix()},  // 黄色，纳入
				{Status: 0, Latency: 9999, Timestamp: now.Unix()}, // 红色，不纳入
				{Status: 1, Latency: 300, Timestamp: now.Unix()},  // 绿色，纳入
			},
			expectedLatency: 200, // (100+200+300)/3 = 200
			description:     "红色状态的超高延迟不应影响平均值",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 调用 buildTimeline（使用 now 作为 endTime，模拟动态滑动窗口）
			timeline := h.buildTimeline(tt.records, now, "24h", 0.7, nil)

			// 找到有数据的 bucket（最后一个，因为所有记录时间戳都是 now）
			var latency int
			for _, point := range timeline {
				if point.Status != -1 { // 有数据的 bucket
					latency = point.Latency
					break
				}
			}

			if latency != tt.expectedLatency {
				t.Errorf("%s: 期望延迟 %dms，实际 %dms",
					tt.description, tt.expectedLatency, latency)
			}
		})
	}
}

// TestBuildTimelineLatencyRounding 测试延迟四舍五入
func TestBuildTimelineLatencyRounding(t *testing.T) {
	h := &Handler{
		config: &config.AppConfig{
			DegradedWeight: 0.7,
		},
	}

	now := time.Now()

	// 测试四舍五入：(100+101)/2 = 100.5 应该四舍五入为 101
	records := []*storage.ProbeRecord{
		{Status: 1, Latency: 100, Timestamp: now.Unix()},
		{Status: 1, Latency: 101, Timestamp: now.Unix()},
	}

	timeline := h.buildTimeline(records, now, "24h", 0.7, nil)

	var latency int
	for _, point := range timeline {
		if point.Status != -1 {
			latency = point.Latency
			break
		}
	}

	// 100.5 四舍五入应为 101（不是 100）
	if latency != 101 {
		t.Errorf("四舍五入测试失败: 期望 101ms，实际 %dms", latency)
	}
}

// TestAlignTimestamp 测试时间对齐逻辑
func TestAlignTimestamp(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name     string
		input    time.Time
		align    string
		expected time.Time
	}{
		{
			name:     "hour 对齐 - 中间时间",
			input:    time.Date(2024, 1, 15, 17, 48, 30, 0, time.UTC),
			align:    "hour",
			expected: time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC), // 向上取整到 18:00
		},
		{
			name:     "hour 对齐 - 整点时间",
			input:    time.Date(2024, 1, 15, 17, 0, 0, 0, time.UTC),
			align:    "hour",
			expected: time.Date(2024, 1, 15, 17, 0, 0, 0, time.UTC), // 已是整点，保持不变
		},
		{
			name:     "day 对齐 - 中间时间",
			input:    time.Date(2024, 1, 15, 12, 30, 0, 0, time.UTC),
			align:    "day",
			expected: time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC), // 向上取整到明天 00:00
		},
		{
			name:     "day 对齐 - 午夜时间",
			input:    time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			align:    "day",
			expected: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), // 已是午夜，保持不变
		},
		{
			name:     "无对齐 - 保持原值",
			input:    time.Date(2024, 1, 15, 12, 30, 45, 123, time.UTC),
			align:    "",
			expected: time.Date(2024, 1, 15, 12, 30, 45, 123, time.UTC), // 保持不变
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.alignTimestamp(tt.input, tt.align)
			if !result.Equal(tt.expected) {
				t.Errorf("alignTimestamp(%v, %q) = %v, want %v",
					tt.input, tt.align, result, tt.expected)
			}
		})
	}
}

// TestParseTimeRange7d30dDayAlign 测试 7d/30d 自动按天对齐
func TestParseTimeRange7d30dDayAlign(t *testing.T) {
	h := &Handler{}

	// 测试 7d 和 30d 的 endTime 是否按天对齐（向上取整到明天 00:00 UTC）
	// 注意：由于使用 time.Now()，我们只验证 endTime 是否为 00:00:00 UTC

	t.Run("7d 自动按天对齐", func(t *testing.T) {
		_, endTime := h.parseTimeRange("7d", "")
		// endTime 应该是某天的 00:00:00 UTC
		if endTime.Hour() != 0 || endTime.Minute() != 0 || endTime.Second() != 0 {
			t.Errorf("7d endTime 应为整天 00:00:00 UTC，实际为 %v", endTime)
		}
	})

	t.Run("30d 自动按天对齐", func(t *testing.T) {
		_, endTime := h.parseTimeRange("30d", "")
		// endTime 应该是某天的 00:00:00 UTC
		if endTime.Hour() != 0 || endTime.Minute() != 0 || endTime.Second() != 0 {
			t.Errorf("30d endTime 应为整天 00:00:00 UTC，实际为 %v", endTime)
		}
	})

	t.Run("7d/30d 忽略 align 参数", func(t *testing.T) {
		// 即使传入 align="hour"，7d 也应该使用 day 对齐
		_, endTime7d := h.parseTimeRange("7d", "hour")
		_, endTime30d := h.parseTimeRange("30d", "hour")

		if endTime7d.Hour() != 0 || endTime7d.Minute() != 0 {
			t.Errorf("7d 应忽略 align=hour 参数，使用 day 对齐，实际 endTime=%v", endTime7d)
		}
		if endTime30d.Hour() != 0 || endTime30d.Minute() != 0 {
			t.Errorf("30d 应忽略 align=hour 参数，使用 day 对齐，实际 endTime=%v", endTime30d)
		}
	})
}

// --- availabilityWeight ---

func TestAvailabilityWeight(t *testing.T) {
	cases := []struct {
		name   string
		status int
		weight float64
		want   float64
	}{
		{"green", 1, 0.7, 1.0},
		{"yellow_default", 2, 0.7, 0.7},
		{"yellow_custom", 2, 0.5, 0.5},
		{"red", 0, 0.7, 0.0},
		{"gray", -1, 0.7, 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := availabilityWeight(tc.status, tc.weight)
			if got != tc.want {
				t.Errorf("availabilityWeight(%d, %f) = %f, want %f", tc.status, tc.weight, got, tc.want)
			}
		})
	}
}

func TestStatusToAvailability(t *testing.T) {
	cases := []struct {
		name   string
		status int
		weight float64
		want   float64
	}{
		{"green", 1, 0.7, 100.0},
		{"yellow", 2, 0.7, 70.0},
		{"red", 0, 0.7, 0.0},
		{"gray", -1, 0.7, -1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statusToAvailability(tc.status, tc.weight)
			if got != tc.want {
				t.Errorf("statusToAvailability(%d, %f) = %f, want %f", tc.status, tc.weight, got, tc.want)
			}
		})
	}
}

// --- availability calculation in buildTimeline ---

func TestBuildTimelineAvailability(t *testing.T) {
	h := &Handler{
		config: &config.AppConfig{DegradedWeight: 0.7},
	}
	now := time.Now()

	tests := []struct {
		name             string
		records          []*storage.ProbeRecord
		degradedWeight   float64
		wantAvailability float64 // expected availability percentage
	}{
		{
			name: "all_green",
			records: []*storage.ProbeRecord{
				{Status: 1, Latency: 100, Timestamp: now.Unix()},
				{Status: 1, Latency: 100, Timestamp: now.Unix()},
			},
			degradedWeight:   0.7,
			wantAvailability: 100.0, // (1.0+1.0)/2 * 100
		},
		{
			name: "all_red",
			records: []*storage.ProbeRecord{
				{Status: 0, Latency: 100, Timestamp: now.Unix()},
				{Status: 0, Latency: 100, Timestamp: now.Unix()},
			},
			degradedWeight:   0.7,
			wantAvailability: 0.0,
		},
		{
			name: "all_yellow_default_weight",
			records: []*storage.ProbeRecord{
				{Status: 2, Latency: 100, Timestamp: now.Unix()},
				{Status: 2, Latency: 100, Timestamp: now.Unix()},
			},
			degradedWeight:   0.7,
			wantAvailability: 70.0, // (0.7+0.7)/2 * 100
		},
		{
			name: "mixed_green_yellow_red",
			records: []*storage.ProbeRecord{
				{Status: 1, Latency: 100, Timestamp: now.Unix()}, // weight 1.0
				{Status: 2, Latency: 200, Timestamp: now.Unix()}, // weight 0.7
				{Status: 0, Latency: 300, Timestamp: now.Unix()}, // weight 0.0
			},
			degradedWeight: 0.7,
			// (1.0 + 0.7 + 0.0) / 3 * 100 = 56.666...
			wantAvailability: (1.0 + 0.7 + 0.0) / 3.0 * 100,
		},
		{
			name: "custom_degraded_weight",
			records: []*storage.ProbeRecord{
				{Status: 2, Latency: 100, Timestamp: now.Unix()},
			},
			degradedWeight:   0.5,
			wantAvailability: 50.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeline := h.buildTimeline(tt.records, now, "24h", tt.degradedWeight, nil)

			var avail float64 = -1
			for _, point := range timeline {
				if point.Status != -1 {
					avail = point.Availability
					break
				}
			}

			if math.Abs(avail-tt.wantAvailability) > 0.01 {
				t.Errorf("availability: want %.2f, got %.2f", tt.wantAvailability, avail)
			}
		})
	}
}

// --- status counts in buildTimeline ---

func TestBuildTimelineStatusCounts(t *testing.T) {
	h := &Handler{
		config: &config.AppConfig{DegradedWeight: 0.7},
	}
	now := time.Now()

	records := []*storage.ProbeRecord{
		{Status: 1, SubStatus: storage.SubStatusNone, Latency: 100, Timestamp: now.Unix()},
		{Status: 1, SubStatus: storage.SubStatusNone, Latency: 100, Timestamp: now.Unix()},
		{Status: 2, SubStatus: storage.SubStatusSlowLatency, Latency: 500, Timestamp: now.Unix()},
		{Status: 0, SubStatus: storage.SubStatusServerError, HttpCode: 500, Latency: 50, Timestamp: now.Unix()},
		{Status: 0, SubStatus: storage.SubStatusRateLimit, HttpCode: 429, Latency: 30, Timestamp: now.Unix()},
	}

	timeline := h.buildTimeline(records, now, "24h", 0.7, nil)

	var counts storage.StatusCounts
	for _, point := range timeline {
		if point.Status != -1 {
			counts = point.StatusCounts
			break
		}
	}

	if counts.Available != 2 {
		t.Errorf("Available: want 2, got %d", counts.Available)
	}
	if counts.Degraded != 1 {
		t.Errorf("Degraded: want 1, got %d", counts.Degraded)
	}
	if counts.Unavailable != 2 {
		t.Errorf("Unavailable: want 2, got %d", counts.Unavailable)
	}
	if counts.SlowLatency != 1 {
		t.Errorf("SlowLatency: want 1, got %d", counts.SlowLatency)
	}
	if counts.ServerError != 1 {
		t.Errorf("ServerError: want 1, got %d", counts.ServerError)
	}
	if counts.RateLimit != 1 {
		t.Errorf("RateLimit: want 1, got %d", counts.RateLimit)
	}
}

// --- empty records ---

func TestBuildTimelineEmptyRecords(t *testing.T) {
	h := &Handler{
		config: &config.AppConfig{DegradedWeight: 0.7},
	}
	now := time.Now()

	timeline := h.buildTimeline(nil, now, "24h", 0.7, nil)

	if len(timeline) != 24 {
		t.Fatalf("expected 24 buckets for 24h period, got %d", len(timeline))
	}

	for i, point := range timeline {
		if point.Status != -1 {
			t.Errorf("bucket %d: expected status -1 (missing), got %d", i, point.Status)
		}
		if point.Availability != -1 {
			t.Errorf("bucket %d: expected availability -1, got %f", i, point.Availability)
		}
	}
}

// --- cache ---

func TestStatusCache(t *testing.T) {
	cache := newStatusCache(100*time.Millisecond, 10)

	// Miss then fill
	data, err := cache.loadWithTTL("key1", 100*time.Millisecond, func() ([]byte, error) {
		return []byte("value1"), nil
	})
	if err != nil {
		t.Fatalf("loadWithTTL: %v", err)
	}
	if string(data) != "value1" {
		t.Errorf("want value1, got %s", string(data))
	}

	// Hit from cache (loader should not be called)
	data2, err := cache.loadWithTTL("key1", 100*time.Millisecond, func() ([]byte, error) {
		t.Fatal("loader should not be called on cache hit")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("loadWithTTL: %v", err)
	}
	if string(data2) != "value1" {
		t.Errorf("want value1, got %s", string(data2))
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	data3, err := cache.loadWithTTL("key1", 100*time.Millisecond, func() ([]byte, error) {
		return []byte("value2"), nil
	})
	if err != nil {
		t.Fatalf("loadWithTTL after expiry: %v", err)
	}
	if string(data3) != "value2" {
		t.Errorf("want value2 after expiry, got %s", string(data3))
	}
}

func TestStatusCacheClear(t *testing.T) {
	cache := newStatusCache(10*time.Second, 10)

	_, _ = cache.loadWithTTL("key1", 10*time.Second, func() ([]byte, error) {
		return []byte("v1"), nil
	})

	cache.clear()

	// After clear, loader should be called again
	called := false
	_, _ = cache.loadWithTTL("key1", 10*time.Second, func() ([]byte, error) {
		called = true
		return []byte("v2"), nil
	})
	if !called {
		t.Error("loader should be called after cache clear")
	}
}

// 缓存被未过期条目填满后，新 key 仍要能写入（淘汰最早到期的一条），
// 否则请求方用随机参数填满缓存即可让合法请求永远直达数据库。
func TestStatusCacheEvictsOldestWhenFull(t *testing.T) {
	cache := newStatusCache(10*time.Second, 3)

	cache.setWithTTL("oldest", []byte("a"), 5*time.Second)
	cache.setWithTTL("k2", []byte("b"), 10*time.Second)
	cache.setWithTTL("k3", []byte("c"), 10*time.Second)

	cache.set("fresh", []byte("d"))

	if data, ok := cache.get("fresh"); !ok || string(data) != "d" {
		t.Fatalf("new key must be cached when full, got ok=%v data=%q", ok, data)
	}
	if _, ok := cache.get("oldest"); ok {
		t.Error("entry with earliest expiry should have been evicted")
	}
	for _, k := range []string{"k2", "k3"} {
		if _, ok := cache.get(k); !ok {
			t.Errorf("entry %q should survive eviction", k)
		}
	}
	if n := len(cache.entries); n != 3 {
		t.Errorf("cache size: want 3, got %d", n)
	}
}

func TestStatusCacheZeroSizeDisablesCaching(t *testing.T) {
	cache := newStatusCache(10*time.Second, 0)
	cache.set("k", []byte("v"))
	if _, ok := cache.get("k"); ok {
		t.Error("cache with maxSize 0 must not store entries")
	}
}

// --- incrementStatusCount ---

func TestIncrementStatusCount(t *testing.T) {
	var counts storage.StatusCounts

	incrementStatusCount(&counts, 1, storage.SubStatusNone, 200)
	incrementStatusCount(&counts, 2, storage.SubStatusSlowLatency, 200)
	incrementStatusCount(&counts, 0, storage.SubStatusServerError, 500)
	incrementStatusCount(&counts, 0, storage.SubStatusAuthError, 401)
	incrementStatusCount(&counts, 0, storage.SubStatusInvalidRequest, 400)
	incrementStatusCount(&counts, 0, storage.SubStatusRateLimit, 429)
	incrementStatusCount(&counts, 0, storage.SubStatusClientError, 404)
	incrementStatusCount(&counts, 0, storage.SubStatusNetworkError, 0)
	incrementStatusCount(&counts, 0, storage.SubStatusContentMismatch, 200)

	if counts.Available != 1 {
		t.Errorf("Available: want 1, got %d", counts.Available)
	}
	if counts.Degraded != 1 {
		t.Errorf("Degraded: want 1, got %d", counts.Degraded)
	}
	if counts.Unavailable != 7 {
		t.Errorf("Unavailable: want 7, got %d", counts.Unavailable)
	}
	if counts.SlowLatency != 1 {
		t.Errorf("SlowLatency: want 1, got %d", counts.SlowLatency)
	}
	if counts.ServerError != 1 {
		t.Errorf("ServerError: want 1, got %d", counts.ServerError)
	}
	if counts.AuthError != 1 {
		t.Errorf("AuthError: want 1, got %d", counts.AuthError)
	}
	if counts.InvalidRequest != 1 {
		t.Errorf("InvalidRequest: want 1, got %d", counts.InvalidRequest)
	}
	if counts.RateLimit != 1 {
		t.Errorf("RateLimit: want 1, got %d", counts.RateLimit)
	}
	if counts.ClientError != 1 {
		t.Errorf("ClientError: want 1, got %d", counts.ClientError)
	}
	if counts.NetworkError != 1 {
		t.Errorf("NetworkError: want 1, got %d", counts.NetworkError)
	}
	if counts.ContentMismatch != 1 {
		t.Errorf("ContentMismatch: want 1, got %d", counts.ContentMismatch)
	}
}

// TestApplyBoardOverrides_PSCPropagation 测试自动移板 override 的 PSC 传播逻辑
func TestApplyBoardOverrides_PSCPropagation(t *testing.T) {
	// 创建 autoMover 并注入 override
	svc := automove.NewService(nil, &config.AppConfig{})
	svc.SetOverrides(map[storage.MonitorKey]automove.MonitorOverride{
		{Provider: "acme", Service: "cc", Channel: "vip", Model: ""}: {
			Board:        "secondary",
			SponsorLevel: config.SponsorLevelPulse,
		},
	})

	h := &Handler{
		config:    &config.AppConfig{},
		autoMover: svc,
	}

	monitors := []config.ServiceConfig{
		// root 监测项（无 parent，无 model）→ 精确匹配
		{Provider: "acme", Service: "cc", Channel: "vip", Board: "hot", SponsorLevel: config.SponsorLevelBackbone},
		// 子模型 1（有 parent，有 model）→ PSC 回退继承
		{Provider: "acme", Service: "cc", Channel: "vip", Model: "gpt-4o", Parent: "acme/cc/vip", Board: "hot", SponsorLevel: config.SponsorLevelBackbone},
		// 子模型 2 → PSC 回退继承
		{Provider: "acme", Service: "cc", Channel: "vip", Model: "claude-4", Parent: "acme/cc/vip", Board: "hot", SponsorLevel: config.SponsorLevelBackbone},
		// 不受影响的通道
		{Provider: "other", Service: "cc", Channel: "free", Board: "hot"},
	}

	result := h.applyBoardOverrides(monitors, nil, 0)

	// root 被降级
	if result[0].Board != "secondary" {
		t.Errorf("root board: want secondary, got %s", result[0].Board)
	}
	if result[0].SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("root sponsor_level: want pulse, got %s", result[0].SponsorLevel)
	}

	// 子模型 1 继承 override
	if result[1].Board != "secondary" {
		t.Errorf("child[gpt-4o] board: want secondary, got %s", result[1].Board)
	}
	if result[1].SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("child[gpt-4o] sponsor_level: want pulse, got %s", result[1].SponsorLevel)
	}

	// 子模型 2 继承 override
	if result[2].Board != "secondary" {
		t.Errorf("child[claude-4] board: want secondary, got %s", result[2].Board)
	}

	// 不受影响的通道保持原值
	if result[3].Board != "hot" {
		t.Errorf("unrelated board: want hot, got %s", result[3].Board)
	}

	// 原始 monitors 未被修改（防御性拷贝）
	if monitors[0].Board != "hot" {
		t.Errorf("original monitors should not be mutated")
	}
}

// TestApplyBoardOverrides_ColdReasonPropagation 测试冷板原因传播到 root 和子模型
func TestApplyBoardOverrides_ColdReasonPropagation(t *testing.T) {
	svc := automove.NewService(nil, &config.AppConfig{})
	svc.SetOverrides(map[storage.MonitorKey]automove.MonitorOverride{
		{Provider: "acme", Service: "cc", Channel: "vip", Model: ""}: {
			Board:      "cold",
			ColdReason: "7天可用率 5.0% 低于自动冷板阈值 10%，已自动移入冷板",
		},
	})

	h := &Handler{
		config:    &config.AppConfig{},
		autoMover: svc,
	}

	monitors := []config.ServiceConfig{
		{Provider: "acme", Service: "cc", Channel: "vip", Board: "hot"},
		{Provider: "acme", Service: "cc", Channel: "vip", Model: "gpt-4o", Parent: "acme/cc/vip", Board: "hot"},
	}

	result := h.applyBoardOverrides(monitors, nil, 0)

	if result[0].Board != "cold" || result[0].ColdReason == "" {
		t.Fatalf("root: board=%s cold_reason=%q", result[0].Board, result[0].ColdReason)
	}
	if result[1].Board != "cold" || result[1].ColdReason != result[0].ColdReason {
		t.Fatalf("child: board=%s cold_reason=%q (expected same as root)", result[1].Board, result[1].ColdReason)
	}
}

// TestApplyBoardOverrides_BoardReasonPropagation 测试质量移板机器码与触发模型名传播到 root 和子模型
func TestApplyBoardOverrides_BoardReasonPropagation(t *testing.T) {
	svc := automove.NewService(nil, &config.AppConfig{})
	svc.SetOverrides(map[storage.MonitorKey]automove.MonitorOverride{
		{Provider: "acme", Service: "cc", Channel: "vip", Model: ""}: {
			Board:                "secondary",
			BoardReason:          "quality_hardfail",
			QualityTriggerModels: "claude-opus",
		},
	})

	h := &Handler{
		config:    &config.AppConfig{},
		autoMover: svc,
	}

	monitors := []config.ServiceConfig{
		{Provider: "acme", Service: "cc", Channel: "vip", Board: "hot"},
		{Provider: "acme", Service: "cc", Channel: "vip", Model: "gpt-4o", Parent: "acme/cc/vip", Board: "hot"},
	}

	result := h.applyBoardOverrides(monitors, nil, 0)

	if result[0].Board != "secondary" || result[0].BoardReason != "quality_hardfail" || result[0].BoardReasonModels != "claude-opus" {
		t.Fatalf("root: board=%s board_reason=%q board_reason_models=%q", result[0].Board, result[0].BoardReason, result[0].BoardReasonModels)
	}
	if result[1].BoardReason != "quality_hardfail" || result[1].BoardReasonModels != "claude-opus" {
		t.Fatalf("child: board_reason=%q board_reason_models=%q (expected same as root)", result[1].BoardReason, result[1].BoardReasonModels)
	}
}

// TestApplyBoardOverrides_ClearsColdReasonOnNonCold 测试非冷板 override 清除旧 ColdReason
func TestApplyBoardOverrides_ClearsColdReasonOnNonCold(t *testing.T) {
	svc := automove.NewService(nil, &config.AppConfig{})
	svc.SetOverrides(map[storage.MonitorKey]automove.MonitorOverride{
		{Provider: "acme", Service: "cc", Channel: "vip", Model: ""}: {Board: "secondary"},
	})

	h := &Handler{
		config:    &config.AppConfig{},
		autoMover: svc,
	}

	monitors := []config.ServiceConfig{
		{Provider: "acme", Service: "cc", Channel: "vip", Board: "cold", ColdReason: "历史原因"},
	}

	result := h.applyBoardOverrides(monitors, nil, 0)
	if result[0].Board != "secondary" {
		t.Fatalf("board=%s, want secondary", result[0].Board)
	}
	if result[0].ColdReason != "" {
		t.Fatalf("cold_reason=%q, want empty", result[0].ColdReason)
	}
}

// TestApplyBoardOverrides_RootWithModel 测试 root 有 model 但无 parent 时走精确匹配
func TestApplyBoardOverrides_RootWithModel(t *testing.T) {
	svc := automove.NewService(nil, &config.AppConfig{})
	svc.SetOverrides(map[storage.MonitorKey]automove.MonitorOverride{
		{Provider: "acme", Service: "cc", Channel: "vip", Model: ""}: {Board: "secondary"},
	})

	h := &Handler{
		config:    &config.AppConfig{},
		autoMover: svc,
	}

	monitors := []config.ServiceConfig{
		// root 无 model → 精确匹配命中
		{Provider: "acme", Service: "cc", Channel: "vip", Board: "hot"},
		// root 有 model 但无 parent → 精确匹配未命中，也不走 PSC 回退
		{Provider: "acme", Service: "cc", Channel: "vip", Model: "special", Board: "hot"},
	}

	result := h.applyBoardOverrides(monitors, nil, 0)

	if result[0].Board != "secondary" {
		t.Errorf("root without model: want secondary, got %s", result[0].Board)
	}
	// root 有 model 无 parent 时，不应该被 PSC 回退影响
	if result[1].Board != "hot" {
		t.Errorf("root with model (no parent): want hot (unchanged), got %s", result[1].Board)
	}
}

// TestApplyBoardOverrides_NoOverrides 测试无 override 时返回原始 slice
func TestApplyBoardOverrides_NoOverrides(t *testing.T) {
	svc := automove.NewService(nil, &config.AppConfig{})
	// 不设置 overrides

	h := &Handler{
		config:    &config.AppConfig{},
		autoMover: svc,
	}

	monitors := []config.ServiceConfig{
		{Provider: "acme", Service: "cc", Channel: "vip", Board: "hot"},
	}

	result := h.applyBoardOverrides(monitors, nil, 0)

	// 无 override 时应返回原始 slice（不拷贝）
	if &result[0] != &monitors[0] {
		t.Errorf("should return original slice when no overrides")
	}
}

// TestApplyBoardOverrides_NilAutoMover 测试 autoMover 为 nil 时直接返回
func TestApplyBoardOverrides_NilAutoMover(t *testing.T) {
	h := &Handler{
		config: &config.AppConfig{},
	}

	monitors := []config.ServiceConfig{
		{Provider: "acme", Service: "cc", Channel: "vip", Board: "hot"},
	}

	result := h.applyBoardOverrides(monitors, nil, 0)
	if &result[0] != &monitors[0] {
		t.Errorf("should return original slice when autoMover is nil")
	}
}
