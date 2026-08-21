package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/probe"
)

// registerSyntheticTestTypes 注册两个合成 service 的探测类型供本文件使用。
//
// 刻意不用真实的 cc/cx/gm：探测类型注册表是包级全局的，动真实 service 会把污染留给同包
// 其它测试（GetOnboardingMeta 就遍历整张表下发）。清理走 probe.UnregisterTestType 真正删除
// ——早先版本是"重新注册一个空壳"假装清理，那样 zz/zy 会永久留在 ListTestTypes 里，
// 后续任何断言 service 集合的测试都会被它们影响。
func registerSyntheticTestTypes(t *testing.T) {
	t.Helper()

	probe.RegisterTestType(&probe.TestType{
		ID:             "zz",
		Name:           "合成服务 zz",
		DefaultVariant: "zz-a",
		Variants: []*probe.PayloadVariant{
			{ID: "zz-a", Filename: "zz-a.json", Order: 1},
			{ID: "zz-b", Filename: "zz-b.json", Order: 2},
		},
	})
	probe.RegisterTestType(&probe.TestType{
		ID:             "zy",
		Name:           "合成服务 zy",
		DefaultVariant: "zy-a",
		Variants: []*probe.PayloadVariant{
			{ID: "zy-a", Filename: "zy-a.json", Order: 1},
		},
	})

	t.Cleanup(func() {
		probe.UnregisterTestType("zz")
		probe.UnregisterTestType("zy")
	})
}

// TestSyntheticTestTypesLeaveNoResidue 守护上面的清理确实是"删除"而不是"留个空壳"：
// 合成 service 一旦残留在包级注册表里，就会经 /api/onboarding/meta 下发给前端。
func TestSyntheticTestTypesLeaveNoResidue(t *testing.T) {
	t.Run("注册期间可见", func(t *testing.T) {
		registerSyntheticTestTypes(t)
		if _, ok := probe.GetTestType("zz"); !ok {
			t.Fatal("合成 service zz 未注册成功")
		}
	})

	// 子测试结束后 cleanup 已跑，注册表必须干净
	if _, ok := probe.GetTestType("zz"); ok {
		t.Error("合成 service zz 在清理后仍留在全局注册表里")
	}
	if _, ok := probe.GetTestType("zy"); ok {
		t.Error("合成 service zy 在清理后仍留在全局注册表里")
	}
}

// newChangeTestHandler 造一个只带运行时配置的 Handler。
// 运行时配置里同一个 PSC 放了父行与子行、且模型不同，用于锁死"只认父行"。
//
// ⚠️ 子行**刻意排在父行前面**：若按 slice 顺序取第一条命中的行就会拿到子行，
// 于是"跳过 Parent 非空"那条判断一旦被删，断言立刻变红（父行在前则该断言是真空的）。
func newChangeTestHandler() *Handler {
	return &Handler{
		config: &config.AppConfig{
			Monitors: []config.ServiceConfig{
				{
					Provider:    "acme",
					Service:     "zz",
					Channel:     "o-nat-glm",
					Parent:      "acme/zz/o-nat-glm",
					Model:       "glm-4.7-air",
					ModelVendor: "zhipu",
				},
				{
					Provider:     "acme",
					Service:      "zz",
					Channel:      "o-nat-glm",
					Model:        "glm-4.7",
					RequestModel: "glm-4.7-250xxx",
					ModelVendor:  "zhipu",
					Template:     "zz-a",
					BaseURL:      "https://old.example.com",
				},
				// service 未在 probe 注册表登记的通道（历史脏数据 / 模板目录被删），
				// 用于覆盖"服务类型未注册探测模板"那道闸。
				{
					Provider: "acme",
					Service:  "zx",
					Channel:  "o-plain",
					Model:    "whatever",
					BaseURL:  "https://old.example.com",
				},
			},
		},
	}
}

// callBuildChangeTestConfig 在测试 context 上跑一次 buildChangeTestConfig，
// 返回 cfg、ok、HTTP 状态码与响应体。
func callBuildChangeTestConfig(h *Handler, req inlineTestRequest) (config.ServiceConfig, bool, int, string) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	cfg, ok := h.buildChangeTestConfig(c, req)
	return cfg, ok, w.Code, w.Body.String()
}

// validChangeTestRequest 返回一份能通过全部校验的请求，供各用例按需改坏其中一项。
func validChangeTestRequest() inlineTestRequest {
	return inlineTestRequest{
		ServiceType:  "zz",
		TemplateName: "zz-b",
		BaseURL:      "https://new.example.com",
		APIKey:       "sk-new-rotated-key",
		TargetKey:    "acme--zz--o-nat-glm",
	}
}

// TestBuildChangeTestConfig_TakesModelFromRuntimeRoot 是本轮修复的核心断言：
// 变更测试的模型三项来自服务端运行时**父行**，base_url/api_key 用请求里变更后的值。
//
// 修复前 /api/change/test 走的是不带 Model 的虚拟 Submission，套 native 模板的通道
// （模型是行级填的）在 wire 上发出 `"model": ""`，轮换 key / 改 base_url 一律测不过。
func TestBuildChangeTestConfig_TakesModelFromRuntimeRoot(t *testing.T) {
	registerSyntheticTestTypes(t)
	h := newChangeTestHandler()

	cfg, ok, code, _ := callBuildChangeTestConfig(h, validChangeTestRequest())
	if !ok {
		t.Fatalf("构造失败，HTTP %d", code)
	}

	// 模型来自父行——尤其 Model 不能是空串，也不能是子行的 glm-4.7-air
	if cfg.Model != "glm-4.7" {
		t.Errorf("Model = %q，期望父行的 glm-4.7（子行是 glm-4.7-air，空串则是修复前的缺陷形态）", cfg.Model)
	}
	if cfg.RequestModel != "glm-4.7-250xxx" {
		t.Errorf("RequestModel = %q，期望父行的 glm-4.7-250xxx", cfg.RequestModel)
	}
	if cfg.ModelVendor != "zhipu" {
		t.Errorf("ModelVendor = %q，期望父行的 zhipu", cfg.ModelVendor)
	}

	// 本次变更值必须用请求里的，不能被父行的旧值覆盖
	if cfg.BaseURL != "https://new.example.com" {
		t.Errorf("BaseURL = %q，期望请求里变更后的 https://new.example.com", cfg.BaseURL)
	}
	if cfg.APIKey != "sk-new-rotated-key" {
		t.Errorf("APIKey = %q，期望请求里的新 key", cfg.APIKey)
	}

	// 模板跟随请求（用户可在测试步切变体排障）
	if cfg.Template != "zz-b" {
		t.Errorf("Template = %q，期望请求指定的 zz-b", cfg.Template)
	}

	// PSC 取自父行
	if cfg.Provider != "acme" || cfg.Service != "zz" || cfg.Channel != "o-nat-glm" {
		t.Errorf("PSC = %s/%s/%s，期望 acme/zz/o-nat-glm", cfg.Provider, cfg.Service, cfg.Channel)
	}
}

// TestBuildChangeTestConfig_ClientCannotSupplyModel 锁死"模型不信客户端"：
// 请求体里塞 model/request_model/model_vendor 也进不了 inlineTestRequest（结构体没这些字段），
// 最终 cfg 仍是运行时父行的值。
//
// 走 json.Unmarshal 而非 bindInlineTestRequest：后者的 SSRF 守卫要做真实 DNS 解析，测试机上
// 解析不了合成域名；而本用例要证的性质在结构体定义与构造函数里，与 bind 无关。
func TestBuildChangeTestConfig_ClientCannotSupplyModel(t *testing.T) {
	registerSyntheticTestTypes(t)
	h := newChangeTestHandler()

	body := `{"service_type":"zz","template_name":"zz-b","base_url":"https://new.example.com",` +
		`"api_key":"sk-new","target_key":"acme--zz--o-nat-glm",` +
		`"model":"claude-opus-4-8","request_model":"claude-opus-4-8","model_vendor":"anthropic"}`

	var req inlineTestRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	cfg, ok, code, _ := callBuildChangeTestConfig(h, req)
	if !ok {
		t.Fatalf("构造失败，HTTP %d", code)
	}
	if cfg.Model != "glm-4.7" || cfg.RequestModel != "glm-4.7-250xxx" || cfg.ModelVendor != "zhipu" {
		t.Errorf("客户端伪造的模型字段被采信了: Model=%q RequestModel=%q ModelVendor=%q",
			cfg.Model, cfg.RequestModel, cfg.ModelVendor)
	}
}

// TestBuildChangeTestConfig_Rejects 覆盖全部拒绝分支——每条都必须 400 且不产出 cfg。
// 关键不变量：任何一条走不通时都**不允许**回落到"空 model 照样探一次"。
//
// ⚠️ 每条都断言**具体错误文案**，不能只断言 400：各道闸的失败态会互相兜底
// （例如通道找不到时 root 是零值，紧接着的 service_type 比对也会 400），
// 只看状态码的话删掉其中一道闸测试照样绿——bite-test 实测过这个真空。
func TestBuildChangeTestConfig_Rejects(t *testing.T) {
	registerSyntheticTestTypes(t)

	tests := []struct {
		name    string
		mutate  func(*inlineTestRequest)
		wantMsg string
	}{
		{
			name:    "缺 target_key",
			mutate:  func(r *inlineTestRequest) { r.TargetKey = "" },
			wantMsg: "缺少 target_key",
		},
		{
			name:    "target_key 只有空白",
			mutate:  func(r *inlineTestRequest) { r.TargetKey = "   " },
			wantMsg: "缺少 target_key",
		},
		{
			name:    "target_key 段数不对",
			mutate:  func(r *inlineTestRequest) { r.TargetKey = "acme--zz" },
			wantMsg: "target_key 格式无效",
		},
		{
			name:    "target_key 有空段",
			mutate:  func(r *inlineTestRequest) { r.TargetKey = "acme----o-nat-glm" },
			wantMsg: "target_key 格式无效",
		},
		{
			name:    "通道不存在",
			mutate:  func(r *inlineTestRequest) { r.TargetKey = "acme--zz--nonexistent" },
			wantMsg: "target_key 对应的通道不存在",
		},
		{
			name:    "PSC 的 provider 段对不上",
			mutate:  func(r *inlineTestRequest) { r.TargetKey = "other--zz--o-nat-glm" },
			wantMsg: "target_key 对应的通道不存在",
		},
		{
			name:    "service_type 与 target_key 不匹配",
			mutate:  func(r *inlineTestRequest) { r.ServiceType = "zy" },
			wantMsg: "service_type 与 target_key 不匹配",
		},
		{
			name: "通道所属 service 未注册探测模板",
			mutate: func(r *inlineTestRequest) {
				r.TargetKey = "acme--zx--o-plain"
				r.ServiceType = "zx"
			},
			wantMsg: "未注册探测模板",
		},
		{
			name:    "模板属于别的 service",
			mutate:  func(r *inlineTestRequest) { r.TemplateName = "zy-a" },
			wantMsg: "测试模板无效",
		},
		{
			name:    "模板不存在",
			mutate:  func(r *inlineTestRequest) { r.TemplateName = "zz-nope" },
			wantMsg: "测试模板无效",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newChangeTestHandler()
			req := validChangeTestRequest()
			tc.mutate(&req)

			cfg, ok, code, body := callBuildChangeTestConfig(h, req)
			if ok {
				t.Fatalf("期望拒绝，却构造出了 cfg: %+v", cfg)
			}
			if code != http.StatusBadRequest {
				t.Errorf("HTTP = %d，期望 400", code)
			}
			if !strings.Contains(body, tc.wantMsg) {
				t.Errorf("错误文案未命中 %q，实际响应: %s", tc.wantMsg, body)
			}
		})
	}
}

// TestBuildChangeTestConfig_RuntimeConfigNotReady 运行时配置未就绪时返回 503 而非 400——
// 那是服务端自身状态问题，不是客户端请求问题。
func TestBuildChangeTestConfig_RuntimeConfigNotReady(t *testing.T) {
	registerSyntheticTestTypes(t)
	h := &Handler{}

	_, ok, code, _ := callBuildChangeTestConfig(h, validChangeTestRequest())
	if ok {
		t.Fatal("运行时配置为 nil 时不应构造成功")
	}
	if code != http.StatusServiceUnavailable {
		t.Errorf("HTTP = %d，期望 503", code)
	}
}

// TestBuildOnboardingTestConfig_NeedsNoTargetKey 回归：收录流程共用同一个请求结构体，
// 但不该被变更流程的 target_key 要求波及。
func TestBuildOnboardingTestConfig_NeedsNoTargetKey(t *testing.T) {
	cfg := buildOnboardingTestConfig(inlineTestRequest{
		ServiceType:  "cc",
		TemplateName: "cc-haiku-arith",
		BaseURL:      "https://relay.example.com",
		APIKey:       "sk-test",
	})

	if cfg.Template != "cc-haiku-arith" {
		t.Errorf("Template = %q", cfg.Template)
	}
	if cfg.BaseURL != "https://relay.example.com" || cfg.APIKey != "sk-test" {
		t.Errorf("BaseURL/APIKey 未透传: %q / %q", cfg.BaseURL, cfg.APIKey)
	}
	if cfg.Service != "cc" {
		t.Errorf("Service = %q，期望 cc", cfg.Service)
	}
}
