package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sha256Hex 返回明文的 sha256 十六进制摘要，与拒绝名单的哈希空间一致。
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// writeRevokedList 在给定目录写一份拒绝名单文件，返回文件名（非全路径——配置里填的是文件名）。
func writeRevokedList(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatalf("write revoked list: %v", err)
	}
	return name
}

// --- 路径解析 ---

// TestResolveRevokedKeyFilePath_AcceptsDirectChild 只接受主配置目录下的直接子文件名。
// 这条约束让「配置能加载到的文件」与「非递归 watcher 能监听到的文件」是同一集合。
func TestResolveRevokedKeyFilePath_AcceptsDirectChild(t *testing.T) {
	got, err := resolveRevokedKeyFilePath("/etc/rp", "revoked_keys.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join("/etc/rp", "revoked_keys.txt"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// TestResolveRevokedKeyFilePath_EmptyMeansDisabled 空值=功能关闭，不报错。
func TestResolveRevokedKeyFilePath_EmptyMeansDisabled(t *testing.T) {
	for _, in := range []string{"", "   "} {
		got, err := resolveRevokedKeyFilePath("/etc/rp", in)
		if err != nil || got != "" {
			t.Fatalf("in=%q → (%q, %v), want (\"\", nil)", in, got, err)
		}
	}
}

// TestResolveRevokedKeyFilePath_RejectsEscapes 绝对路径、子目录、父目录穿越一律拒绝。
func TestResolveRevokedKeyFilePath_RejectsEscapes(t *testing.T) {
	for _, in := range []string{
		"/etc/passwd",             // 绝对路径
		"sub/revoked_keys.txt",    // 子目录
		"../revoked_keys.txt",     // 父目录穿越
		"./sub/../../revoked.txt", // 绕路穿越
		".",                       // 目录自身
	} {
		if got, err := resolveRevokedKeyFilePath("/etc/rp", in); err == nil {
			t.Fatalf("in=%q 应被拒绝，却得到 %q", in, got)
		}
	}
}

// --- 名单加载 ---

// TestLoadRevokedKeyFile_ParsesEntries 注释、空行、大写 hex 都要正确处理，结果统一小写。
func TestLoadRevokedKeyFile_ParsesEntries(t *testing.T) {
	dir := t.TempDir()
	a, b := sha256Hex("leaked-key-a"), sha256Hex("leaked-key-b")
	name := writeRevokedList(t, dir, "revoked_keys.txt", strings.Join([]string{
		"# 注释行",
		"",
		"   ",
		strings.ToUpper(a),
		b,
	}, "\n"))

	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = name
	cfg.ChangeRequests.RevokedKeyCount = 2
	if err := cfg.loadRevokedKeyFile(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	set := cfg.ChangeRequests.RevokedKeySHA256
	if len(set) != 2 {
		t.Fatalf("len(set) = %d, want 2", len(set))
	}
	for _, want := range []string{a, b} {
		if _, ok := set[want]; !ok {
			t.Fatalf("摘要 %s… 未进入名单（大写 hex 应被归一化为小写）", want[:8])
		}
	}
}

// TestLoadRevokedKeyFile_DisabledWhenEmpty 未配置路径时名单为空且不报错。
func TestLoadRevokedKeyFile_DisabledWhenEmpty(t *testing.T) {
	cfg := &AppConfig{}
	if err := cfg.loadRevokedKeyFile(t.TempDir()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ChangeRequests.RevokedKeySHA256 != nil {
		t.Fatalf("未配置时名单应为 nil，实际 %v 条", len(cfg.ChangeRequests.RevokedKeySHA256))
	}
}

// TestLoadRevokedKeyFile_CountMismatchRejected 条目数与 revoked_key_count 不一致必须拒绝整次加载。
// 这道闸挡的是「名单被部分写入/意外截断」——静默接受一份变短的名单等于让部分泄露 key 复活。
func TestLoadRevokedKeyFile_CountMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	name := writeRevokedList(t, dir, "revoked_keys.txt", sha256Hex("only-one")+"\n")

	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = name
	cfg.ChangeRequests.RevokedKeyCount = 2 // 期望 2 条，实际 1 条
	err := cfg.loadRevokedKeyFile(dir)
	if err == nil {
		t.Fatal("条目数不匹配时必须报错")
	}
	if cfg.ChangeRequests.RevokedKeySHA256 != nil {
		t.Fatal("加载失败时不得留下半份名单")
	}
}

// TestLoadRevokedKeyFile_RequiresPositiveCount 配了文件却没配期望条目数 → 拒绝（否则截断无从发现）。
func TestLoadRevokedKeyFile_RequiresPositiveCount(t *testing.T) {
	dir := t.TempDir()
	name := writeRevokedList(t, dir, "revoked_keys.txt", sha256Hex("k")+"\n")

	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = name
	if err := cfg.loadRevokedKeyFile(dir); err == nil {
		t.Fatal("未配置 revoked_key_count 时必须报错")
	}
}

// TestLoadRevokedKeyFile_RejectsMalformedLineWithoutEchoing 非法行要报错，
// 且错误文本只带行号、绝不回显该行内容（该行可能就是一把明文密钥）。
func TestLoadRevokedKeyFile_RejectsMalformedLineWithoutEchoing(t *testing.T) {
	const secretish = "sk-this-would-be-a-plaintext-key"
	dir := t.TempDir()
	name := writeRevokedList(t, dir, "revoked_keys.txt", sha256Hex("ok")+"\n"+secretish+"\n")

	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = name
	cfg.ChangeRequests.RevokedKeyCount = 2
	err := cfg.loadRevokedKeyFile(dir)
	if err == nil {
		t.Fatal("非法 hex 行必须报错")
	}
	if strings.Contains(err.Error(), secretish) {
		t.Fatalf("错误信息回显了行内容，可能泄露明文: %v", err)
	}
	if !strings.Contains(err.Error(), "2") {
		t.Fatalf("错误信息应指出行号 2，实际: %v", err)
	}
}

// TestLoadRevokedKeyFile_RejectsWrongLengthHex 长度不足 32 字节的合法 hex 也要拒（截半的摘要）。
func TestLoadRevokedKeyFile_RejectsWrongLengthHex(t *testing.T) {
	dir := t.TempDir()
	name := writeRevokedList(t, dir, "revoked_keys.txt", sha256Hex("k")[:32]+"\n")

	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = name
	cfg.ChangeRequests.RevokedKeyCount = 1
	if err := cfg.loadRevokedKeyFile(dir); err == nil {
		t.Fatal("非 32 字节摘要必须报错")
	}
}

// TestLoadRevokedKeyFile_RejectsMissingFile 配了路径但文件不存在 → 报错（不静默降级成空名单）。
func TestLoadRevokedKeyFile_RejectsMissingFile(t *testing.T) {
	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = "nope.txt"
	cfg.ChangeRequests.RevokedKeyCount = 1
	if err := cfg.loadRevokedKeyFile(t.TempDir()); err == nil {
		t.Fatal("文件不存在必须报错")
	}
}

// TestLoadRevokedKeyFile_RejectsNonRegularFile 目录/符号链接等非普通文件要拒。
func TestLoadRevokedKeyFile_RejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "revoked_keys.txt"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = "revoked_keys.txt"
	cfg.ChangeRequests.RevokedKeyCount = 1
	if err := cfg.loadRevokedKeyFile(dir); err == nil {
		t.Fatal("非普通文件必须报错")
	}
}

// TestLoadRevokedKeyFile_RejectsOversizeFile 超出体积上限要拒（防 FIFO/巨文件把加载拖死）。
func TestLoadRevokedKeyFile_RejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	line := sha256Hex("x") + "\n"
	body := strings.Repeat(line, revokedKeyFileMaxBytes/len(line)+2)
	name := writeRevokedList(t, dir, "revoked_keys.txt", body)

	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = name
	cfg.ChangeRequests.RevokedKeyCount = 1
	if err := cfg.loadRevokedKeyFile(dir); err == nil {
		t.Fatal("超大文件必须报错")
	}
}

// --- 与 Loader.Load 的集成 ---

// onboarding 的 admin_token/encryption_key/proof_secret 是 onboarding 与 change_requests
// 共享的必填项（见 normalizeOnboardingConfig），change_requests 单开也必须给齐。
const revokedListConfigBody = `
change_requests:
  enabled: true
  revoked_key_file: "revoked_keys.txt"
  revoked_key_count: 1
onboarding:
  enabled: true
  admin_token: "test-admin-token"
  encryption_key: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  proof_secret: "test-proof-secret"
`

// TestLoad_WiresRevokedKeyList 端到端：Load 之后名单必须已进入运行时配置。
func TestLoad_WiresRevokedKeyList(t *testing.T) {
	path := writeInlineConfig(t, revokedListConfigBody)
	dir := filepath.Dir(path)
	want := sha256Hex("leaked")
	writeRevokedList(t, dir, "revoked_keys.txt", want+"\n")

	cfg, err := (&Loader{}).Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.ChangeRequests.RevokedKeySHA256[want]; !ok {
		t.Fatalf("名单未接入运行时配置，实际 %d 条", len(cfg.ChangeRequests.RevokedKeySHA256))
	}
}

// TestLoad_FailsOnBadRevokedKeyList 名单坏了要让整次加载失败（fail-closed）：
// 启动期=拒绝启动，热更新期=整份配置跳过并保留上一份（含上一份的名单）。
func TestLoad_FailsOnBadRevokedKeyList(t *testing.T) {
	path := writeInlineConfig(t, revokedListConfigBody)
	writeRevokedList(t, filepath.Dir(path), "revoked_keys.txt", "not-a-hash\n")

	if _, err := (&Loader{}).Load(path); err == nil {
		t.Fatal("名单非法时 Load 必须失败")
	}
}

// --- watcher 判定 ---

// TestWatcher_CurrentRevokedKeyPath watcher 必须把名单文件认成"该触发热更新"的文件，
// 否则单独更新名单永远不生效。未配置/无生效配置时返回空串（不误放行任何事件）。
func TestWatcher_CurrentRevokedKeyPath(t *testing.T) {
	dir := "/etc/rp"

	// 尚无生效配置
	w := &Watcher{loader: &Loader{}}
	if got := w.currentRevokedKeyPath(dir); got != "" {
		t.Fatalf("无生效配置时应返回空串，实际 %q", got)
	}

	// 生效配置未配名单
	loader := &Loader{}
	loader.currentConfig = &AppConfig{}
	w = &Watcher{loader: loader}
	if got := w.currentRevokedKeyPath(dir); got != "" {
		t.Fatalf("未配置名单时应返回空串，实际 %q", got)
	}

	// 生效配置配了名单
	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = "revoked_keys.txt"
	loader.currentConfig = cfg
	if got, want := w.currentRevokedKeyPath(dir), filepath.Join(dir, "revoked_keys.txt"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	// 生效配置里的名单路径非法（越界）→ 空串，绝不放行目录外事件
	cfg.ChangeRequests.RevokedKeyFile = "../evil.txt"
	if got := w.currentRevokedKeyPath(dir); got != "" {
		t.Fatalf("非法路径应返回空串，实际 %q", got)
	}
}

// TestLoadRevokedKeyFile_RejectsSymlink 符号链接也要拒（Lstat 不跟随），
// 否则名单可被指向目录外的任意文件。
func TestLoadRevokedKeyFile_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte(sha256Hex("k")+"\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "revoked_keys.txt")); err != nil {
		t.Skipf("本环境不支持 symlink: %v", err)
	}

	cfg := &AppConfig{}
	cfg.ChangeRequests.RevokedKeyFile = "revoked_keys.txt"
	cfg.ChangeRequests.RevokedKeyCount = 1
	if err := cfg.loadRevokedKeyFile(dir); err == nil {
		t.Fatal("符号链接必须被拒")
	}
}

// TestLoadOrRollback_KeepsPreviousRevokedList 名单坏掉时热更新必须保留**上一份含名单的配置**，
// 而不是降级成一份没有名单的配置——后者等于全部泄露 key 复活。
func TestLoadOrRollback_KeepsPreviousRevokedList(t *testing.T) {
	path := writeInlineConfig(t, revokedListConfigBody)
	dir := filepath.Dir(path)
	want := sha256Hex("leaked")
	writeRevokedList(t, dir, "revoked_keys.txt", want+"\n")

	loader := &Loader{}
	if _, err := loader.Load(path); err != nil {
		t.Fatalf("首次 Load: %v", err)
	}

	// 把名单写坏，走热更新路径
	writeRevokedList(t, dir, "revoked_keys.txt", "not-a-hash\n")
	cfg, err := loader.loadOrRollback(path)
	if err == nil {
		t.Fatal("坏名单必须报错")
	}
	if cfg == nil {
		t.Fatal("应返回上一份配置而非 nil")
	}
	if _, ok := cfg.ChangeRequests.RevokedKeySHA256[want]; !ok {
		t.Fatalf("回滚后必须仍持有上一份名单，实际 %d 条", len(cfg.ChangeRequests.RevokedKeySHA256))
	}
}

// TestLoad_FirstActivationFailureLeavesNoList 首次启用名单若加载失败，运行态是「没有名单」
// ——这不是相对安全目标的 fail-closed。固化这个已知边界：部署名单必须验证它真的进了运行态
// （见 docs/user/config.md 的运维说明），别指望热更新失败会保护你。
func TestLoad_FirstActivationFailureLeavesNoList(t *testing.T) {
	path := writeInlineConfig(t, revokedListConfigBody)
	writeRevokedList(t, filepath.Dir(path), "revoked_keys.txt", "not-a-hash\n")

	loader := &Loader{}
	cfg, err := loader.loadOrRollback(path)
	if err == nil {
		t.Fatal("坏名单必须报错")
	}
	if cfg != nil {
		t.Fatalf("首次激活失败时没有可回退的配置，应返回 nil，实际非 nil（名单 %d 条）",
			len(cfg.ChangeRequests.RevokedKeySHA256))
	}
}
