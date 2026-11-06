// Package urlutil 提供 URL 比较的小工具，供 onboarding 与 change 两条自助流程共享，作为
// 「base_url ↔ test_api_url host/port 一致性」校验的单一真相源——避免该校验在两处各自实现而漂移
// （历史上正因两份各自只比 host、漏比端口，可在一个端口测出 proof 却把 base_url 应用成同 host 另一端口）。
package urlutil

import (
	"fmt"
	"net/url"
	"strings"
)

// SameHostPort 判断两个 URL 是否指向同一 host+端口（端口按各自 scheme 默认归一化：
// https→443、http→80、显式端口优先）。用于把 test_api_url 与将要落地的 base_url 绑定到同一来源，
// 比只比 hostname 更严：拦下「同 host 换端口」的绕过。
func SameHostPort(a, b *url.URL) bool {
	return strings.EqualFold(a.Hostname(), b.Hostname()) && normalizedPort(a) == normalizedPort(b)
}

// SameEndpoint 在 SameHostPort 之上再比 scheme、路径与查询串，即「两个 URL 指向同一个接入点」。
//
// 只比 host/port 会漏掉**路径替换**：拿 https://edge/good 测出 proof，提交
// base_url=https://edge/other，两者 host/port 相同、却是完全不同的上游——而中转商的多线路
// 恰恰常按路径区分。收录与变更的提交校验都用这一条。
//
// 路径按「剥尾部斜杠」归一（https://h 与 https://h/ 视为同一个），其余部分逐字比较：
// 大小写、编码形式的差异一律视为不同，因为它们对上游而言本就可能是不同资源。
// scheme 也必须相同：SameHostPort 只把端口按 scheme 归一化，故 http://h:443 与 https://h
// 在它眼里是同一个，而实际一个明文一个加密。
func SameEndpoint(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		SameHostPort(a, b) &&
		sameUserinfo(a, b) &&
		normalizedPath(a) == normalizedPath(b) &&
		a.RawQuery == b.RawQuery
}

// sameUserinfo 比较 URL 里内嵌的 user[:password]。
//
// 它是端点身份的一部分而不是装饰：`https://alice@edge/x` 与 `https://bob@edge/x` 打的是同一台
// 机器、却可能是两个账户，HTTP 客户端会据此生成不同的 Authorization 头。忽略它就等于允许
// 「用甲账户测通、报乙账户」。两边都没有内嵌凭据（绝大多数情况）时恒等。
func sameUserinfo(a, b *url.URL) bool {
	return a.User.String() == b.User.String()
}

// normalizedPath 返回剥掉尾部斜杠的路径（根路径与空路径同归一为空串）。
func normalizedPath(u *url.URL) string {
	return strings.TrimSuffix(u.EscapedPath(), "/")
}

// normalizedPort 返回 URL 的有效端口：显式端口优先，缺省时按 scheme 默认（https→443 / http→80）。
func normalizedPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

// ValidatePublicBaseShape 拒绝公开自助流程里带查询串、fragment 或 userinfo 的 base_url。
//
// 当前生产配置没有一条用到这三样，公开自助流程也不支持；它们却是拿回显服务冒充模型接口的
// 抓手：`https://httpbin.org/anything?RP_ANSWER=20&…&RP_ANSWER=198` 把全部可能的答案塞进
// 查询串，回显里必含正确答案，算术题照样判绿（2026-11 生产日志实证）。查询串连空 `?` 也拒、
// fragment 连空 `#` 也拒：模板按字符串拼 `{{BASE_URL}}/v1/messages`，`https://h/base?` 或
// `https://h/base#x` 会把后缀挤进 query/fragment，实际请求的仍是 `/base`；而 SameEndpoint
// 不比较这两样，测试与提交两侧都得在入口挡住（这一条是代码评审发现的绕过，非日志实证）。
//
// 只用于公开的收录/变更流程；管理员路径与调度器不受约束。
func ValidatePublicBaseShape(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("base_url 无效")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("base_url 不能带查询参数（?…），请填写接口根地址")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("base_url 不能带 # 片段，请填写接口根地址")
	}
	if u.User != nil {
		return fmt.Errorf("base_url 不能内嵌用户名或密码")
	}
	return nil
}
