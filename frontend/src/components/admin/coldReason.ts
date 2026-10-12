/**
 * 冷板原因输入上限（软约束，只挡手滑粘贴长文）。
 * 后端刻意不做长度拒绝：cold_reason 只有管理员写得了（用户自助变更的字段白名单不含它），
 * 属 internal/displayname 注释里写明的「管理员逃生口」一档。取 80 是因为它最终显示在
 * 状态页通道名的 hover tooltip 里，再长就撑破提示框。
 */
export const COLD_REASON_MAX_LENGTH = 80;
