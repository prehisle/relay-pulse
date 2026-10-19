package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// monitors.d 写路径的展示名预检：撞名的文件不能落盘。

func namedMonitorYAML(provider, channel, providerName, channelName string, extra ...string) string {
	lines := []string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"monitors:",
		"  - provider: " + provider,
		"    service: cc",
		"    channel: " + channel,
		"    provider_name: \"" + providerName + "\"",
		"    channel_name: \"" + channelName + "\"",
		"    template: cc-haiku-tiny",
		"    base_url: https://api.example.com",
	}
	return strings.Join(append(lines, extra...), "\n")
}

func namedMonitorFile(provider, channel, providerName, channelName string) *MonitorFile {
	return &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{{
			Provider: provider, Service: "cc", Channel: channel,
			ProviderName: providerName, ChannelName: channelName,
			Template: "cc-haiku-tiny", BaseURL: "https://api.example.com",
		}},
	}
}

func newTestStore(t *testing.T) (*MonitorStore, string) {
	t.Helper()
	configDir, _ := setupTestMonitorsDir(t)
	dir := filepath.Join(configDir, MonitorsDirName)
	return NewMonitorStore(dir), configDir
}

func requireDisplayNameConflict(t *testing.T, err error) {
	t.Helper()
	var conflict *DisplayNameConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("期望 *DisplayNameConflictError，实际 %v", err)
	}
}

func TestCreate_RejectsDisplayNameTakenByAnotherChannel(t *testing.T) {
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max", namedMonitorYAML("codekey", "o-max", "CodeKey", "O-Max"))

	err := store.Create(namedMonitorFile("codekey", "o-max-main", "CodeKey", " O-Max "))

	requireDisplayNameConflict(t, err)
	if _, statErr := os.Stat(filepath.Join(store.Dir(), "codekey--cc--o-max-main.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("被拒的文件不应落盘，stat err=%v", statErr)
	}
}

func TestCreate_AllowsDistinctDisplayName(t *testing.T) {
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max", namedMonitorYAML("codekey", "o-max", "CodeKey", "O-Max"))

	if err := store.Create(namedMonitorFile("codekey", "o-max-main", "CodeKey", "O-Max-2")); err != nil {
		t.Fatalf("不同展示名应允许创建：%v", err)
	}
}

func TestUpdate_RejectsRenameIntoTakenDisplayName(t *testing.T) {
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max", namedMonitorYAML("codekey", "o-max", "CodeKey", "O-Max"))
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max-main", namedMonitorYAML("codekey", "o-max-main", "CodeKey", "O-Max-2"))
	path := filepath.Join(store.Dir(), "codekey--cc--o-max-main.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = store.Update("codekey--cc--o-max-main", namedMonitorFile("codekey", "o-max-main", "CodeKey", "O-Max"), 1)

	requireDisplayNameConflict(t, err)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("被拒的更新不应改动磁盘文件（含 revision）")
	}
}

func TestUpdate_KeepingOwnDisplayNameIsNotAConflict(t *testing.T) {
	// 目标文件自己的旧内容不能被当成"另一条通道"。
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max", namedMonitorYAML("codekey", "o-max", "CodeKey", "O-Max"))

	if err := store.Update("codekey--cc--o-max", namedMonitorFile("codekey", "o-max", "CodeKey", "O-Max"), 1); err != nil {
		t.Fatalf("原样保存不应被拒：%v", err)
	}
}

func TestCreate_ChildInheritsParentDisplayNameBeforeCheck(t *testing.T) {
	// 子行展示名为空，继承父行的 "O-Max"；另一条通道恰好以父通道的 slug "o-max" 为展示名。
	// 若不继承、按 slug 回退，会误判子行与它冲突。
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--legacy", namedMonitorYAML("codekey", "legacy", "CodeKey", "o-max"))

	file := namedMonitorFile("codekey", "o-max", "CodeKey", "O-Max")
	file.Monitors[0].Model = "opus"
	file.Monitors = append(file.Monitors, ServiceConfig{Parent: "codekey/cc/o-max", Model: "sonnet"})

	if err := store.Create(file); err != nil {
		t.Fatalf("子行继承父展示名后不应冲突：%v", err)
	}
}

func TestCreate_ChildWithOwnTakenDisplayNameIsRejected(t *testing.T) {
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max", namedMonitorYAML("codekey", "o-max", "CodeKey", "O-Max"))

	file := namedMonitorFile("codekey", "o-max-main", "CodeKey", "O-Max-2")
	file.Monitors[0].Model = "opus"
	file.Monitors = append(file.Monitors, ServiceConfig{Parent: "codekey/cc/o-max-main", Model: "sonnet", ChannelName: "O-Max"})

	requireDisplayNameConflict(t, store.Create(file))
}

func TestCreate_ExistingConflictElsewhereDoesNotBlockUnrelatedWrite(t *testing.T) {
	// 磁盘上已有两条撞名的通道（旧状态），与本次写入无关，不能因此拒绝。
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max", namedMonitorYAML("codekey", "o-max", "CodeKey", "O-Max"))
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max-main", namedMonitorYAML("codekey", "o-max-main", "CodeKey", "O-Max"))

	if err := store.Create(namedMonitorFile("novaris", "o-max", "Novaris", "O-Max")); err != nil {
		t.Fatalf("与既有冲突无关的写入不应被拒：%v", err)
	}
}

func TestUpdate_CanFixExistingConflict(t *testing.T) {
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max", namedMonitorYAML("codekey", "o-max", "CodeKey", "O-Max"))
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max-main", namedMonitorYAML("codekey", "o-max-main", "CodeKey", "O-Max"))

	if err := store.Update("codekey--cc--o-max-main", namedMonitorFile("codekey", "o-max-main", "CodeKey", "O-Max-2"), 1); err != nil {
		t.Fatalf("改名修复既有冲突的写入不应被拒：%v", err)
	}
}

func TestCreate_UnreadableFileDoesNotBlockWrite(t *testing.T) {
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "broken--cc--x", "monitors: [this is: not valid")

	if err := store.Create(namedMonitorFile("codekey", "o-max", "CodeKey", "O-Max")); err != nil {
		t.Fatalf("读不出的文件不应堵住无关写入：%v", err)
	}
}

func TestCreate_DisabledChannelStillHoldsItsDisplayName(t *testing.T) {
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "91token--cc--o-max-main",
		namedMonitorYAML("91token", "o-max-main", "91token", "O-Max", "    disabled: true", "    hidden: true"))

	requireDisplayNameConflict(t, store.Create(namedMonitorFile("91token", "o-max-main2", "91token", "O-Max")))
}

func TestUpdate_ChildRenamedIntoTakenDisplayNameIsRejected(t *testing.T) {
	// 只改子行的独立展示名。目标文件旧内容若参与比较，旧根与新根同路径会被判成父行歧义，
	// 新子行解析不了而漏检。
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max", namedMonitorYAML("codekey", "o-max", "CodeKey", "Taken"))
	writeTestMonitorFile(t, configDir, "codekey--cc--o-max-main", namedMonitorYAML("codekey", "o-max-main", "CodeKey", "O-Max-2",
		"    model: opus", "  - parent: codekey/cc/o-max-main", "    model: sonnet"))

	file := namedMonitorFile("codekey", "o-max-main", "CodeKey", "O-Max-2")
	file.Monitors[0].Model = "opus"
	file.Monitors = append(file.Monitors, ServiceConfig{Parent: "codekey/cc/o-max-main", Model: "sonnet", ChannelName: "Taken"})

	requireDisplayNameConflict(t, store.Update("codekey--cc--o-max-main", file, 1))
}

func TestCreate_FileWithoutSinglePSCDoesNotBlockWrite(t *testing.T) {
	// YAML 合法但混了两个通道的根行：loader 会拒它，这里不能拿它挡无关写入。
	store, configDir := newTestStore(t)
	writeTestMonitorFile(t, configDir, "mixed--cc--a", namedMonitorYAML("mixed", "a", "Mixed", "O-Max",
		"  - provider: mixed", "    service: cc", "    channel: b", "    channel_name: Other"))

	if err := store.Create(namedMonitorFile("mixed", "c", "Mixed", "O-Max")); err != nil {
		t.Fatalf("推不出唯一 PSC 的文件不应堵住无关写入：%v", err)
	}
}
