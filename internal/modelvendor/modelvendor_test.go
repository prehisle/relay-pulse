package modelvendor

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty is allowed", "", "", false},
		{"whitespace only is allowed", "   ", "", false},
		{"known code unchanged", "zhipu", "zhipu", false},
		{"trims surrounding space", "  zhipu  ", "zhipu", false},
		{"lowercases", "ZHIPU", "zhipu", false},
		{"trims and lowercases", " ZhiPu\t", "zhipu", false},
		{"another known code", "anthropic", "anthropic", false},
		{"unknown but well-formed code rejected", "cohere", "", true},
		{"interior space rejected", "zhi pu", "", true},
		{"underscore rejected", "zhi_pu", "", true},
		{"leading hyphen rejected", "-zhipu", "", true},
		{"trailing hyphen rejected", "zhipu-", "", true},
		{"non-ascii rejected", "智谱", "", true},
		{"over max length rejected", strings.Repeat("a", MaxCodeLen+1), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Normalize(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Normalize(%q)=%q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestNormalizeSeparatesFormatErrorFromUnknownCode 锁死错误分层：MaxCodeLen 之内的合法格式即使
// 未收录也只报「未知」，超长才报「格式无效」——两类错误的处置动作不同（拼错 vs 需扩词表）。
func TestNormalizeSeparatesFormatErrorFromUnknownCode(t *testing.T) {
	atLimit := strings.Repeat("a", MaxCodeLen)
	if _, err := Normalize(atLimit); err == nil || !strings.Contains(err.Error(), "未知") {
		t.Fatalf("恰好 %d 位的合法格式应报「未知」，实际 err=%v", MaxCodeLen, err)
	}
	if _, err := Normalize(atLimit + "a"); err == nil || !strings.Contains(err.Error(), "格式") {
		t.Fatalf("超过 %d 位应报「格式无效」，实际 err=%v", MaxCodeLen, err)
	}
}

func TestNormalizeErrorMentionsValue(t *testing.T) {
	_, err := Normalize("cohere")
	if err == nil {
		t.Fatal("Normalize(\"cohere\") 应报错")
	}
	if !strings.Contains(err.Error(), "cohere") {
		t.Fatalf("错误信息应含原值以便定位，实际为 %q", err.Error())
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(""); err != nil {
		t.Fatalf("Validate(\"\") 应放行空值，实际 err=%v", err)
	}
	if err := Validate(" OpenAI "); err != nil {
		t.Fatalf("Validate 应接受可规范化的已知 code，实际 err=%v", err)
	}
	if err := Validate("cohere"); err == nil {
		t.Fatal("Validate 应拒绝词表外的 code")
	}
}

func TestLookup(t *testing.T) {
	v, ok := Lookup("zhipu")
	if !ok {
		t.Fatal("Lookup(\"zhipu\") 应命中")
	}
	if v.Code != "zhipu" || v.Label == "" || v.IconKey == "" {
		t.Fatalf("Lookup(\"zhipu\") 返回不完整条目: %+v", v)
	}

	if _, ok := Lookup(" ANTHROPIC "); !ok {
		t.Fatal("Lookup 应与 Normalize 同款规范化（trim + 转小写）")
	}
	if _, ok := Lookup("cohere"); ok {
		t.Fatal("Lookup(\"cohere\") 不应命中")
	}
	if _, ok := Lookup(""); ok {
		t.Fatal("Lookup(\"\") 不应命中（空值合法但不是一个厂商）")
	}
}

func TestOptionsReturnsDeepCopy(t *testing.T) {
	first := Options()
	if len(first) == 0 {
		t.Fatal("Options() 不应为空")
	}
	original := first[0]

	first[0].Code = "tampered"
	first[0].Label = "tampered"
	first[0].IconKey = "tampered"

	second := Options()
	if second[0] != original {
		t.Fatalf("Options() 必须返回深拷贝：改返回值污染了内部词表，got %+v want %+v", second[0], original)
	}
	if len(second) != len(first) {
		t.Fatalf("Options() 长度不稳定: %d vs %d", len(second), len(first))
	}
}

// TestCatalogInvariants 守住词表本身的不变量：code 唯一、格式合法、展示字段齐全。
// 新增厂商时若违反其一，这里直接变红，不必等到 API 层或前端才发现。
func TestCatalogInvariants(t *testing.T) {
	seen := make(map[string]struct{}, len(Options()))
	for _, v := range Options() {
		if _, dup := seen[v.Code]; dup {
			t.Fatalf("词表 code 重复: %q", v.Code)
		}
		seen[v.Code] = struct{}{}

		if !codePattern.MatchString(v.Code) || len(v.Code) > MaxCodeLen {
			t.Fatalf("词表 code 格式非法: %q", v.Code)
		}
		if v.Code != strings.ToLower(v.Code) {
			t.Fatalf("词表 code 必须为小写: %q", v.Code)
		}
		if strings.TrimSpace(v.Label) == "" {
			t.Fatalf("%q 缺少展示名", v.Code)
		}
		if strings.TrimSpace(v.IconKey) == "" {
			t.Fatalf("%q 缺少图标 key", v.Code)
		}

		got, err := Normalize(v.Code)
		if err != nil || got != v.Code {
			t.Fatalf("词表内 code %q 必须能通过 Normalize，得到 (%q, %v)", v.Code, got, err)
		}

		// 查询索引与词表同源，二者一旦漂移（例如日后有人手写 byCode）这里就变红。
		looked, ok := Lookup(v.Code)
		if !ok || looked != v {
			t.Fatalf("Lookup(%q)=(%+v, %v)，与词表条目 %+v 不一致", v.Code, looked, ok, v)
		}
	}

	for _, want := range []string{"anthropic", "openai", "google", "zhipu", "moonshot", "minimax", "deepseek", "qwen"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("初始词表缺少 %q", want)
		}
	}
}
