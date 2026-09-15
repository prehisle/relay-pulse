package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// subhdr 影子族：一个源模板 + 三份逐字节派生。
//
// 这族在验「中转商是否按 codex 订阅态身份头分叉到不同的网关代码路径」，做法是让影子模板
// 与现役模板**只差身份头**、其余形态一模一样，再比两条序列的可用率与延迟。所以「族内四份
// 除模型串外逐字节相同」不是整洁癖，而是**对照实验的有效性前提**：任何一份被单独改动
// （probe 档位、某个 header、body 里的一个空格）都会让跨模型的对照失去可比性——而这件事
// 目前不会产生任何报错，改一份忘三份是静默失败。本测试就是那个报错。
//
// body 按**原始字节**上 wire（loader 存 json.RawMessage、probe 只做 TrimSpace + 占位符替换），
// 所以比较必须是逐字节的，不能 unmarshal 后比结构——那样会漏掉缩进差异。
//
// 若将来本族解散（删掉源模板），连同本测试一并删除。
const (
	subhdrSourceTemplate = "cx-gpt6astra-subhdr-arith.json"
	subhdrFamilyMarker   = "-subhdr-"
	// subhdrMinLines 防「文件读空/被截断 → 循环零次 → 守卫真空通过」。
	subhdrMinLines = 40
)

// subhdrDerivedTemplates 是源模板之外的族成员。新增派生必须登记在这里，
// 漏登记会被 TestSubhdrShadowFamilyMembersAreAllRegistered 抓住。
var subhdrDerivedTemplates = []string{
	"cx-gpt56-subhdr-arith.json",
	"cx-gpt56luna-subhdr-arith.json",
	"cx-gpt56terra-subhdr-arith.json",
}

// subhdrDivergentKeys 是允许在派生文件里与源不同的顶层字段。前缀带一个 tab 是刻意的：
// body 里也有一个 "model" 键（值为 {{MODEL}}），按 trim 后的前缀匹配会连它一起命中。
var subhdrDivergentKeys = []string{
	"\t\"self_serve_label\": ",
	"\t\"model\": ",
	"\t\"request_model\": ",
	"\t\"_comment\": ",
}

func readSubhdrTemplateLines(t *testing.T, dir, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("读取 subhdr 族模板 %s 失败: %v", name, err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) < subhdrMinLines {
		t.Fatalf("%s 只有 %d 行（期望至少 %d）——文件被截断或路径有误，本守卫可能真空通过",
			name, len(lines), subhdrMinLines)
	}
	return lines
}

// locateSubhdrDivergentLines 返回每个允许差异字段所在的行号。每个字段必须**恰好**命中一行：
// 零次 = 字段被删或缩进从 tab 漂成空格（那样本守卫会把真实差异当成允许差异放过去），
// 多次 = 定位不唯一，同样不能信。
func locateSubhdrDivergentLines(t *testing.T, name string, lines []string) map[int]string {
	t.Helper()
	idx := make(map[int]string, len(subhdrDivergentKeys))
	for _, key := range subhdrDivergentKeys {
		hits := make([]int, 0, 1)
		for i, ln := range lines {
			if strings.HasPrefix(ln, key) {
				hits = append(hits, i)
			}
		}
		if len(hits) != 1 {
			t.Fatalf("%s 中字段 %q 命中 %d 行（期望恰好 1 行）——缩进或字段顺序已漂，本守卫的差异白名单不再可信",
				name, strings.TrimSpace(key), len(hits))
		}
		idx[hits[0]] = key
	}
	return idx
}

// TestSubhdrShadowFamilyIsByteDerivedFromSource 锁死族内的逐字节派生关系。
func TestSubhdrShadowFamilyIsByteDerivedFromSource(t *testing.T) {
	const templatesDir = "../../templates"

	srcLines := readSubhdrTemplateLines(t, templatesDir, subhdrSourceTemplate)
	srcIdx := locateSubhdrDivergentLines(t, subhdrSourceTemplate, srcLines)

	for _, name := range subhdrDerivedTemplates {
		gotLines := readSubhdrTemplateLines(t, templatesDir, name)
		if len(gotLines) != len(srcLines) {
			t.Errorf("%s 有 %d 行、源 %s 有 %d 行——派生关系已断，跨模型对照的形态一致性没了",
				name, len(gotLines), subhdrSourceTemplate, len(srcLines))
			continue
		}
		gotIdx := locateSubhdrDivergentLines(t, name, gotLines)

		for line, key := range srcIdx {
			if gotIdx[line] != key {
				t.Errorf("%s 的字段 %q 不在源的同一行（源第 %d 行）——字段顺序漂了，逐行比对不再有意义",
					name, strings.TrimSpace(key), line+1)
			}
		}
		if t.Failed() {
			continue
		}

		identical := 0
		for i := range srcLines {
			key, allowed := srcIdx[i]
			if allowed {
				// 非真空保障：允许差异的四行必须**确实**不同。少了这条，把派生文件整个
				// 复制成源的副本也能通过本测试，而那种文件会与源撞 DB 业务键。
				if srcLines[i] == gotLines[i] {
					t.Errorf("%s 第 %d 行（%s）与源完全相同——派生必须换掉模型串，否则会与源撞 (provider,service,channel,model) 业务键",
						name, i+1, strings.TrimSpace(key))
				}
				continue
			}
			if srcLines[i] != gotLines[i] {
				t.Errorf("%s 第 %d 行与源 %s 不一致，而该行不在允许差异的白名单内：\n  源: %s\n  本: %s",
					name, i+1, subhdrSourceTemplate, srcLines[i], gotLines[i])
				continue
			}
			identical++
		}
		if identical < subhdrMinLines-len(subhdrDivergentKeys) {
			t.Errorf("%s 只比中 %d 行相同（期望至少 %d）——逐行比对可能没真正跑起来",
				name, identical, subhdrMinLines-len(subhdrDivergentKeys))
		}
	}
}

// TestSubhdrShadowFamilyMembersAreAllRegistered 防止新增派生漏登记：
// templates/ 下所有带 -subhdr- 的文件都必须要么是源、要么在 subhdrDerivedTemplates 里，
// 否则它不受上面那道字节派生守卫保护、能悄悄漂走。
func TestSubhdrShadowFamilyMembersAreAllRegistered(t *testing.T) {
	const templatesDir = "../../templates"

	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("读取内置模板目录失败: %v", err)
	}

	registered := map[string]bool{subhdrSourceTemplate: true}
	for _, name := range subhdrDerivedTemplates {
		registered[name] = true
	}

	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || !strings.Contains(name, subhdrFamilyMarker) {
			continue
		}
		found++
		if !registered[name] {
			t.Errorf("模板 %s 属于 subhdr 影子族却没登记进 subhdrDerivedTemplates——它不受字节派生守卫保护，形态会悄悄漂走",
				name)
		}
	}

	if want := len(registered); found != want {
		t.Errorf("目录里扫到 %d 个 subhdr 族文件、名单里有 %d 个——名单里有文件已被删除（删族成员要同步改名单，删整族要连本测试一起删）",
			found, want)
	}
}
