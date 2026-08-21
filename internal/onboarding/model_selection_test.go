package onboarding

import (
	"context"
	"strings"
	"testing"
)

// TestValidateModelSelection_TemplateFamilyRules 锁死「模板族决定要不要填模型」这条规则。
//
// native 族漏填 = 上线即在 wire 上发 `"model": ""`（v2.79.0 修过的那类恒红）；非 native 多填 =
// 用户以为在测 A、实际在测模板里钉死的 B。两侧都必须是拒绝而非静默兜底。
func TestValidateModelSelection_TemplateFamilyRules(t *testing.T) {
	tests := []struct {
		name       string
		template   string
		model      string
		vendor     string
		wantModel  string
		wantVendor string
		wantErr    string
	}{
		{
			name: "native 模板填了模型与厂商即通过", template: "cc-native-arith-nothink",
			model: "glm-5.2", vendor: "zhipu", wantModel: "glm-5.2", wantVendor: "zhipu",
		},
		{
			name: "native 模板可以不填厂商（词表外厂商留给管理员后补）", template: "cx-native-arith",
			model: "kimi-k2.7-code", vendor: "", wantModel: "kimi-k2.7-code",
		},
		{
			name: "native 模板漏填模型即拒", template: "cc-native-arith",
			model: "  ", vendor: "zhipu", wantErr: "需要填写厂商的模型 ID",
		},
		{
			name: "普通模板另填模型即拒", template: "cc-haiku-arith",
			model: "claude-opus-4-8", wantErr: "已由探针模板确定",
		},
		{
			name: "普通模板不填模型即通过", template: "cc-haiku-arith",
		},
		{
			name: "模型 ID 剥首尾空白", template: "cc-native-arith",
			model: "  glm-5.2\t", wantModel: "glm-5.2",
		},
		{
			name: "词表外厂商即拒", template: "cc-native-arith",
			model: "glm-5.2", vendor: "notavendor", wantErr: "不在受控词表内",
		},
		{
			name: "厂商大小写规范化", template: "cc-native-arith",
			model: "glm-5.2", vendor: " ZhiPu ", wantModel: "glm-5.2", wantVendor: "zhipu",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model, vendor, err := ValidateModelSelection(tc.template, tc.model, tc.vendor)
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
			if model != tc.wantModel || vendor != tc.wantVendor {
				t.Errorf("规范值 = %q/%q，期望 %q/%q", model, vendor, tc.wantModel, tc.wantVendor)
			}
		})
	}
}

// TestValidateModelSelection_ModelIDCharset 模型 ID 白名单是注入边界，不是洁癖。
//
// 模板 body 按原始字节发送、{{MODEL}} 是纯字符串替换，且同一次替换作用于 URL、headers、body
// 与 success_contains 四处：一个双引号就能打烂 body 的 JSON，一个 CRLF 就能在 header 里另起一行。
func TestValidateModelSelection_ModelIDCharset(t *testing.T) {
	accepted := []string{
		"glm-5.2", "kimi-k2.7-code", "deepseek-ai/DeepSeek-V3",
		"us.anthropic.claude-haiku-4-5-20251001-v2:0", "ep-20260101120000-abcde",
		"qwen3-coder-plus", "doubao-seed-2.1-turbo", "gpt-5.6-sol", "a",
	}
	for _, model := range accepted {
		if _, _, err := ValidateModelSelection("cc-native-arith", model, ""); err != nil {
			t.Errorf("真实模型 ID %q 应被接受，实际: %v", model, err)
		}
	}

	rejected := map[string]string{
		`glm"-5.2`:                           "双引号会打烂 body 的 JSON",
		"glm\\5.2":                           "反斜杠是 JSON 转义引导符",
		"glm\r\nX-Evil: 1":                   "CRLF 可在 header 里另起一行",
		"glm 5.2":                            "空格",
		"{{API_KEY}}":                        "占位符会被二次替换",
		"-glm-5.2":                           "以分隔符开头",
		"glm@5.2\x00":                        "控制字符",
		strings.Repeat("a", MaxModelIDLen+1): "超长",
	}
	for model, why := range rejected {
		if _, _, err := ValidateModelSelection("cc-native-arith", model, ""); err == nil {
			t.Errorf("模型 ID %q 应被拒（%s），实际通过", model, why)
		}
	}
}

// TestSubmit_ModelSelectionEnforced 端到端：提交入口确实跑了组合校验，且规范值落到 Submission 上。
func TestSubmit_ModelSelectionEnforced(t *testing.T) {
	svc := newSubmitTestService(t)

	// 「普通模板带模型」这条组合在公开入口已**结构上不可能**：SubmitRequest 自 2026-08-21
	// 起没有 model/model_vendor 字段，客户端塞了也会被 ShouldBindJSON 丢弃。该分支的守卫改由
	// ValidateModelSelection 的单元用例（本文件上方）与 AdminUpdate 用例（下方）覆盖。

	// native 模板漏填模型 → 在提交入口被拒。
	// 这是公开路径的第二道 fail-closed：native 模板本就标了 self_serve_visible:false、
	// 在 handler 的 resolveSelfServeTemplate 处已被挡下，万一有人把它标回可见，这里仍会拒。
	_, err := svc.Submit(context.Background(), &SubmitRequest{
		AgreementAccepted: true,
		ProviderName:      "Prov",
		ServiceType:       "cc",
		TemplateName:      "cc-native-arith",
		ChannelType:       "O",
		ChannelSource:     "nat",
		BaseURL:           "https://api.example.com",
		TestAPIURL:        "https://api.example.com",
	}, "1.2.3.4")
	if err == nil || !strings.Contains(err.Error(), "需要填写厂商的模型 ID") {
		t.Fatalf("native 模板漏填模型应被拒，实际: %v", err)
	}
}

// TestAdminUpdate_ModelSelection 覆盖审核阶段的两个要点：
// ① 组合规则同样生效（把模板改成 native 却没模型 → 拒）；
// ② 模型字段能被**显式清空**——否则模板从 native 改回普通模板后，旧模型永远清不掉、这条申请
//
//	会被组合校验永久卡死。
func TestAdminUpdate_ModelSelection(t *testing.T) {
	ctx := context.Background()
	svc := newSubmitTestService(t)

	sub := &Submission{
		PublicID:     "pub-model-1",
		Status:       StatusPending,
		ProviderName: "Prov",
		ServiceType:  "cc",
		TemplateName: "cc-native-arith",
		Model:        "glm-5.2",
		ModelVendor:  "zhipu",
		ChannelType:  "O",
		ChannelCode:  "o-nat-main",
		BaseURL:      "https://api.example.com",
	}
	if err := svc.store.Save(ctx, sub); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 存取往返：两个新列真的落库了
	got, err := svc.store.GetByPublicID(ctx, "pub-model-1")
	if err != nil || got == nil {
		t.Fatalf("GetByPublicID: %v", err)
	}
	if got.Model != "glm-5.2" || got.ModelVendor != "zhipu" {
		t.Fatalf("落库后 model/vendor = %q/%q，期望 glm-5.2/zhipu", got.Model, got.ModelVendor)
	}

	// ① 改成普通模板却留着模型 → 拒
	if _, err := svc.AdminUpdate(ctx, "pub-model-1", map[string]any{
		"template_name": "cc-haiku-arith",
	}); err == nil || !strings.Contains(err.Error(), "已由探针模板确定") {
		t.Fatalf("模板改普通却留着模型应被拒，实际: %v", err)
	}

	// ② 同一次更新里显式清空模型 → 通过
	updated, err := svc.AdminUpdate(ctx, "pub-model-1", map[string]any{
		"template_name": "cc-haiku-arith",
		"model":         "",
		"model_vendor":  "",
	})
	if err != nil {
		t.Fatalf("显式清空模型应通过，实际: %v", err)
	}
	if updated.Model != "" || updated.ModelVendor != "" {
		t.Errorf("清空后 model/vendor = %q/%q，期望均为空", updated.Model, updated.ModelVendor)
	}

	// ③ 改回 native 模板却没模型 → 拒
	if _, err := svc.AdminUpdate(ctx, "pub-model-1", map[string]any{
		"template_name": "cc-native-arith",
	}); err == nil || !strings.Contains(err.Error(), "需要填写厂商的模型 ID") {
		t.Fatalf("模板改 native 却没模型应被拒，实际: %v", err)
	}
}
