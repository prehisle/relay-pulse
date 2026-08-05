package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/modelvendor"
	"monitor/internal/onboarding"
)

// TestNewMonitorLayerCarriesModelVendor 父层与子层共用同一个构造函数，
// vendor 只在这一处填——「父子两个构造点漏填一处」这类 bug 由此在结构上不可能发生。
func TestNewMonitorLayerCarriesModelVendor(t *testing.T) {
	parent := config.ServiceConfig{Model: "GLM", ModelVendor: "zhipu"}
	child := config.ServiceConfig{Model: "GLM-Air", ModelVendor: "zhipu", Parent: "acme/cc/vip"}

	if got := newMonitorLayer(parent, 0, layerData{}).ModelVendor; got != "zhipu" {
		t.Errorf("父层 ModelVendor = %q, want %q", got, "zhipu")
	}
	if got := newMonitorLayer(child, 1, layerData{}).ModelVendor; got != "zhipu" {
		t.Errorf("子层 ModelVendor = %q, want %q", got, "zhipu")
	}
}

// TestNewMonitorLayerPreservesExistingFields 加字段不得改动既有字段的取值语义。
func TestNewMonitorLayerPreservesExistingFields(t *testing.T) {
	cfg := config.ServiceConfig{Model: "GLM", RequestModel: "glm-4.7"}
	layer := newMonitorLayer(cfg, 3, layerData{})
	if layer.Model != "GLM" {
		t.Errorf("Model = %q, want %q", layer.Model, "GLM")
	}
	if layer.RequestModel != "glm-4.7" {
		t.Errorf("RequestModel = %q, want %q", layer.RequestModel, "glm-4.7")
	}
	if layer.LayerOrder != 3 {
		t.Errorf("LayerOrder = %d, want 3", layer.LayerOrder)
	}
}

// TestMonitorLayerWireOmitsEmptyModelVendor 新字段是 additive 的：存量行 vendor 全空，
// wire 上必须完全不出现该键，既有消费者逐字节看不到任何变化。
func TestMonitorLayerWireOmitsEmptyModelVendor(t *testing.T) {
	empty, err := json.Marshal(MonitorLayer{Model: "GLM"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "model_vendor") {
		t.Fatalf("空 vendor 不该出现在 wire 上，实际 %s", empty)
	}

	filled, err := json.Marshal(MonitorLayer{Model: "GLM", ModelVendor: "zhipu"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(filled), `"model_vendor":"zhipu"`) {
		t.Fatalf("非空 vendor 未出现在 wire 上，实际 %s", filled)
	}
}

// TestOnboardingMetaCarriesModelVendors /api/onboarding/meta 下发厂商词表，
// 与 channel_sources_by_service 同款——前端不自建一份，避免两边漂移。
func TestOnboardingMetaCarriesModelVendors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(nil, &config.AppConfig{}, nil)
	// GetOnboardingMeta 只对 service 做非空判定、不调其方法，零值实例足够走通该分支。
	h.SetOnboardingService(&onboarding.Service{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/onboarding/meta", nil)
	h.GetOnboardingMeta(c)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp OnboardingMetaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.ModelVendors) != len(modelvendor.Options()) {
		t.Fatalf("model_vendors 条目数 = %d, want %d", len(resp.ModelVendors), len(modelvendor.Options()))
	}
	var found bool
	for _, v := range resp.ModelVendors {
		if v.Code == "anthropic" {
			found = true
			if v.Label == "" || v.IconKey == "" {
				t.Errorf("下发条目缺展示字段: %+v", v)
			}
		}
	}
	if !found {
		t.Error("下发的词表里找不到 anthropic")
	}
	if !strings.Contains(w.Body.String(), `"model_vendors"`) {
		t.Error("wire 上缺 model_vendors 键")
	}
}
