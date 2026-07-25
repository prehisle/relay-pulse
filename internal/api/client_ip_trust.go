package api

import (
	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/logger"
)

// applyClientIPTrust 把配置里的可信代理边界装进 gin engine。
//
// 背景：gin 默认信任所有代理并解析 X-Forwarded-For，于是 c.ClientIP() 完全由请求方书写。
// 由于探测限流、收录/变更每日配额与 submitter_ip_hash 审计都以它为计数主体，
// 这个默认值等价于"三道防护同时可绕"。此处把边界收敛为显式配置：
//
//   - TrustedPlatform 非空时 gin 直接采信该 Header（如 Cloudflare 的 CF-Connecting-IP），
//     不再解析 XFF 链——前提是应用端口不可被绕过该入口直连，否则该 Header 同样可伪造。
//   - 其余情况按 TrustedProxies 逐跳解析 XFF，只有来自这些地址的转发才被采信。
//
// 配置非法时 fail-closed 到"不信任任何代理"（ClientIP 退化为 RemoteAddr），
// 而不是保留 gin 的"信任所有"——错误配置下宁可把同机代理后的请求都算成一个来源，
// 也不能让伪造头继续生效。正常路径下 config.validate 已在加载期拦掉非法值。
func applyClientIPTrust(router *gin.Engine, cfg config.ServerConfig) {
	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		logger.Error("api", "可信代理配置非法，已收紧为不信任任何代理",
			"error", err, "trusted_proxies", cfg.TrustedProxies)
		if fallbackErr := router.SetTrustedProxies(nil); fallbackErr != nil {
			logger.Error("api", "收紧可信代理失败", "error", fallbackErr)
		}
		router.TrustedPlatform = ""
		return
	}
	router.TrustedPlatform = cfg.TrustedPlatform
}
