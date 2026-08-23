package onboarding

import (
	"fmt"
	"regexp"
	"strings"
)

// 自助收录字段规范（提交即强制）：
//   - provider_name 为服务商展示名（允许中文等任意可见文本，拒不可见字符，≤100 rune）；发布时机器 slug 从它派生或由 target_provider 覆盖
//   - channel_source 必须是受控词表 ChannelSourceCatalog 中、对应 service 下的 2-5 位小写代码
//   - channel_group 为用户自定义 1-8 位小写分组代号（中转商自己的分组），留空回退 channelGroupDefault
//   - channel_name 为可选的通道展示名（允许中文等任意语言），仅用于 UI 显示，不参与 channel_code/PSC 派生
const channelGroupDefault = "main"

var channelGroupPattern = regexp.MustCompile(`^[a-z0-9]{1,8}$`)

// ChannelGroupRule 返回 channel_group 的校验规则，供前端做同步校验。
func ChannelGroupRule() (pattern, defaultValue string, maxLength int) {
	return channelGroupPattern.String(), channelGroupDefault, 8
}

// normalizeGroup 规范化 channel_group：留空回退默认值，并校验格式。返回小写规范值。
func normalizeGroup(group string) (string, error) {
	group = strings.ToLower(strings.TrimSpace(group))
	if group == "" {
		group = channelGroupDefault
	}
	if !channelGroupPattern.MatchString(group) {
		return "", fmt.Errorf("channel_group 格式无效（%q），应为 1-8 位小写字母或数字", group)
	}
	return group, nil
}

// deriveChannelCode 从通道类型、来源、分组派生通道代码 {type}-{source}-{group}（全小写）。
// group 为空时退化为两段 {type}-{source}，仅用于兼容旧申请与旧 monitors.d/ 通道。
func deriveChannelCode(channelType, channelSource, channelGroup string) string {
	t := strings.ToLower(strings.TrimSpace(channelType))
	source := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(channelSource), " ", ""))
	group := strings.ToLower(strings.TrimSpace(channelGroup))
	if group == "" {
		return fmt.Sprintf("%s-%s", t, source)
	}
	return fmt.Sprintf("%s-%s-%s", t, source, group)
}
