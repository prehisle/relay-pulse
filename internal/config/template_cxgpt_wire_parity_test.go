package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cx-gpt 订阅态族：五份模板共享**同一套 wire 形态**（`headers` 与 `body` 两块逐字节相同），
// 模型差异全部由 `{{MODEL}}` 占位符承担，故只有元数据字段（model / request_model /
// self_serve_* / _comment）允许各不相同。
//
// 为什么必须逐字节比、不能 unmarshal 后比结构：body 按**原始字节**上 wire
// （loader 存 json.RawMessage、probe 只做 TrimSpace + 占位符替换），缩进空白也是 wire 的
// 一部分；headers 同理，多一个头少一个头都会改变网关看到的客户端指纹。
//
// 为什么值得守：这套形态取自一次真 codex CLI 0.154.0 抓包，五份是**手工同步**的。升 CLI
// 版本、改一个身份头、调一处缩进——只改其中一份不会有任何运行时报错，只会让五个模型悄悄
// 跑在两套形态上，而那时任何跨模型的可用率比较都不再可比。本测试就是那个报错。
//
// 形态依据与改动纪律写在 cx-gpt-arith 的 `_comment` 里（族内单一真相源）。
const (
	cxGPTWireSourceTemplate = "cx-gpt-arith.json"

	// cxGPTWireMinHeadersBytes / cxGPTWireMinBodyBytes 防「块提取返回空串或半截 →
	// 比较恒真 → 守卫真空通过」。取值是当前实际长度的一半有余，不贴着写。
	cxGPTWireMinHeadersBytes = 600
	cxGPTWireMinBodyBytes    = 300
)

// cxGPTWireDerivedTemplates 是源之外的族成员。新增订阅态模板必须登记在这里，
// 漏登记会被 TestCxGPTFamilyMembersAreAllClassified 抓住。
var cxGPTWireDerivedTemplates = []string{
	"cx-gpt56-arith.json",
	"cx-gpt56luna-arith.json",
	"cx-gpt56terra-arith.json",
	"cx-gpt6astra-arith.json",
}

// cxGPTPlatformEraTemplates 是**刻意留在旧平台态形态**的 cx-gpt 模板：假 UA
// `Codex-CLI/1.0`、带 `openai-beta: responses=experimental`、probe 档位 5s/10s。
// 2026-09-16 订阅态转正时按站长决定「只转有 A/B 实测依据的 4 个 request_model」，
// gpt-5.4 不在其中。
//
// 豁免必须是**显式登记的决定**，不能靠命名模式碰巧漏掉——否则哪天有人新建一个
// cx-gpt 模板忘了同步形态，守卫会因为「它也没在族名单里」而放过去。
var cxGPTPlatformEraTemplates = []string{
	"cx-gpt54-arith.json",
}

// cxGPTWireRequiredHeaderMarkers 是订阅态身份头的存在性断言。
//
// 这几条是**非真空保障的核心**：没有它们，把五份的 headers 全部替换成 `{}` 也能让
// 「逐字节相同」通过。它们同时钉住了「订阅态」这件事本身的语义——真 codex CLI 发的是
// originator + 整包 turn metadata，缺了就不再是我们 A/B 验证过的那个形态。
var cxGPTWireRequiredHeaderMarkers = []string{
	`"originator": "codex_exec"`,
	`"x-codex-turn-metadata"`,
	`"chatgpt-account-id": "{{STABLE_UUID2}}"`,
	`"{{RAND_UUID_V7}}"`,
	`{{UNIX_MS}}`,
}

// cxGPTWireForbiddenHeaderMarkers 钉死「订阅态抓包里没有的东西不许回来」。
// `openai-beta` 是旧平台态形态的头，2026-09-16 随订阅态一并删除；它悄悄回来
// 意味着有人把某份模板 revert 回了平台态，而那正是本守卫要抓的分裂。
var cxGPTWireForbiddenHeaderMarkers = []string{
	"openai-beta",
	"Codex-CLI/1.0",
}

var cxGPTWireRequiredBodyMarkers = []string{
	`"prompt_cache_key": "{{RAND_UUID_V7}}"`,
	`"model": "{{MODEL}}"`,
	`"{{PROMPT}}"`,
}

// extractJSONObjectBlock 按**原始文本**取出顶层 `"<key>": { … }` 的大括号块。
//
// 刻意不 unmarshal：本测试要比的就是字节，解析再序列化会把缩进差异抹平，
// 而缩进正是最隐蔽的一类漂移。字符串内的大括号与转义引号都不参与配平。
func extractJSONObjectBlock(t *testing.T, name, raw, key string) string {
	t.Helper()

	marker := "\n\t\"" + key + "\": "
	i := strings.Index(raw, marker)
	if i < 0 {
		t.Fatalf("%s 里找不到顶层字段 %q（缩进漂了或字段被删）——本守卫无法比较，视为失败", name, key)
	}
	start := i + len(marker)
	if start >= len(raw) || raw[start] != '{' {
		t.Fatalf("%s 的 %q 不是一个 JSON 对象——形态已变，本守卫的假设不再成立", name, key)
	}

	depth, inStr, esc := 0, false, false
	for j := start; j < len(raw); j++ {
		c := raw[j]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : j+1]
			}
		}
	}
	t.Fatalf("%s 的 %q 大括号不配平——文件被截断", name, key)
	return ""
}

func readCxGPTTemplate(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("读取 cx-gpt 族模板 %s 失败: %v", name, err)
	}
	return string(raw)
}

// assertCxGPTWireShape 检查一份模板的 wire 形态确实是订阅态、且块长度不是空壳。
// 这是「逐字节相同」之外的第二道闸：相同但都错，仍然是错。
func assertCxGPTWireShape(t *testing.T, name, headers, body string) {
	t.Helper()

	if len(headers) < cxGPTWireMinHeadersBytes {
		t.Errorf("%s 的 headers 只有 %d 字节（期望至少 %d）——块提取可能没真正跑起来，或形态被掏空",
			name, len(headers), cxGPTWireMinHeadersBytes)
	}
	if len(body) < cxGPTWireMinBodyBytes {
		t.Errorf("%s 的 body 只有 %d 字节（期望至少 %d）——块提取可能没真正跑起来，或形态被掏空",
			name, len(body), cxGPTWireMinBodyBytes)
	}
	for _, marker := range cxGPTWireRequiredHeaderMarkers {
		if !strings.Contains(headers, marker) {
			t.Errorf("%s 的 headers 缺少订阅态标记 %s——这已经不是 2026-09-16 A/B 验证过的那个形态",
				name, marker)
		}
	}
	for _, marker := range cxGPTWireForbiddenHeaderMarkers {
		if strings.Contains(headers, marker) {
			t.Errorf("%s 的 headers 出现了平台态残留 %q——有人把它 revert 回旧形态了", name, marker)
		}
	}
	for _, marker := range cxGPTWireRequiredBodyMarkers {
		if !strings.Contains(body, marker) {
			t.Errorf("%s 的 body 缺少必需标记 %s", name, marker)
		}
	}
}

// TestCxGPTSubscriptionFamilyShareWireShape 锁死族内 headers/body 的逐字节一致。
func TestCxGPTSubscriptionFamilyShareWireShape(t *testing.T) {
	const templatesDir = "../../templates"

	srcRaw := readCxGPTTemplate(t, templatesDir, cxGPTWireSourceTemplate)
	srcHeaders := extractJSONObjectBlock(t, cxGPTWireSourceTemplate, srcRaw, "headers")
	srcBody := extractJSONObjectBlock(t, cxGPTWireSourceTemplate, srcRaw, "body")
	assertCxGPTWireShape(t, cxGPTWireSourceTemplate, srcHeaders, srcBody)

	for _, name := range cxGPTWireDerivedTemplates {
		raw := readCxGPTTemplate(t, templatesDir, name)
		headers := extractJSONObjectBlock(t, name, raw, "headers")
		body := extractJSONObjectBlock(t, name, raw, "body")
		assertCxGPTWireShape(t, name, headers, body)

		if headers != srcHeaders {
			t.Errorf("%s 的 headers 与源 %s 不是逐字节相同——族内客户端指纹已分裂，跨模型可用率不再可比\n  源 %d 字节 / 本 %d 字节",
				name, cxGPTWireSourceTemplate, len(srcHeaders), len(headers))
		}
		if body != srcBody {
			t.Errorf("%s 的 body 与源 %s 不是逐字节相同——body 按原始字节上 wire，空白差异同样会改变请求\n  源 %d 字节 / 本 %d 字节",
				name, cxGPTWireSourceTemplate, len(srcBody), len(body))
		}
	}
}

// TestCxGPTFamilyMembersAreAllClassified 防止新增 cx-gpt 模板漏登记：
// templates/ 下每个 cx-gpt*.json 都必须要么是订阅态族成员、要么在平台态豁免名单里。
// 漏登记的那个不受形态守卫保护，能悄悄用一套没验证过的形态上生产。
func TestCxGPTFamilyMembersAreAllClassified(t *testing.T) {
	const templatesDir = "../../templates"

	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("读取内置模板目录失败: %v", err)
	}

	classified := map[string]string{cxGPTWireSourceTemplate: "订阅态源"}
	for _, name := range cxGPTWireDerivedTemplates {
		classified[name] = "订阅态派生"
	}
	for _, name := range cxGPTPlatformEraTemplates {
		classified[name] = "平台态豁免"
	}

	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || !strings.HasPrefix(name, "cx-gpt") {
			continue
		}
		found++
		if _, ok := classified[name]; !ok {
			t.Errorf("模板 %s 没有归类：它既不在订阅态族名单里、也不在平台态豁免名单里，因而不受 wire 形态守卫保护。"+
				"新建 cx-gpt 模板时请显式登记到 cxGPTWireDerivedTemplates 或 cxGPTPlatformEraTemplates", name)
		}
	}

	if want := len(classified); found != want {
		t.Errorf("目录里扫到 %d 个 cx-gpt 模板、名单里登记了 %d 个——名单里有文件已被删除（删模板要同步改名单）",
			found, want)
	}
}
