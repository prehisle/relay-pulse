package api

import (
	"strings"
	"testing"

	"monitor/internal/probe"
)

// registerSelfServeVariants 注册一个合成 service，含可见/隐藏/native 三种变体，测试结束即移除。
func registerSelfServeVariants(t *testing.T) {
	t.Helper()

	probe.RegisterTestType(&probe.TestType{
		ID:             "zs",
		Name:           "合成服务 zs",
		DefaultVariant: "zs-visible-arith",
		Variants: []*probe.PayloadVariant{
			{ID: "zs-visible-arith", Filename: "zs-visible-arith.json", Order: 1, SelfServeVisible: true},
			{ID: "zs-internal-arith", Filename: "zs-internal-arith.json", Order: 2, SelfServeVisible: false},
			{ID: "zs-native-arith", Filename: "zs-native-arith.json", Order: 3, SelfServeVisible: true, Native: true},
		},
	})
	probe.RegisterTestType(&probe.TestType{
		ID:             "zt",
		Name:           "合成服务 zt",
		DefaultVariant: "zt-visible-arith",
		Variants: []*probe.PayloadVariant{
			{ID: "zt-visible-arith", Filename: "zt-visible-arith.json", Order: 1, SelfServeVisible: true},
		},
	})

	t.Cleanup(func() {
		probe.UnregisterTestType("zs")
		probe.UnregisterTestType("zt")
	})
}

// TestResolveSelfServeTemplate 锁死公开自助入口的模板闸。
//
// 三件事各自都能独立坏掉，故分别断言：模板得存在、得属于本 service、且得对自助流程可见。
// 最后一条只挡公开入口——管理员改 template_name 上架内部模板是有意保留的逃生口。
func TestResolveSelfServeTemplate(t *testing.T) {
	registerSelfServeVariants(t)

	tests := []struct {
		name     string
		service  string
		template string
		wantErr  string
	}{
		{name: "可见模板通过", service: "zs", template: "zs-visible-arith"},
		{name: "native 模板同样开放自助", service: "zs", template: "zs-native-arith"},
		{name: "内部模板被挡", service: "zs", template: "zs-internal-arith", wantErr: "不开放自助收录"},
		{name: "跨 service 引用被挡", service: "zt", template: "zs-visible-arith", wantErr: "不可用"},
		{name: "不存在的模板被挡", service: "zs", template: "zs-nosuch", wantErr: "不可用"},
		{name: "空模板名被挡", service: "zs", template: "", wantErr: "不可用"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			variant, err := resolveSelfServeTemplate(tc.service, tc.template)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("期望被拒（%s），实际通过", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("拒因 = %v，期望含 %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望通过，实际: %v", err)
			}
			if variant.ID != tc.template {
				t.Errorf("返回变体 = %q，期望 %q", variant.ID, tc.template)
			}
		})
	}
}
