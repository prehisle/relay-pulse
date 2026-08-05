package api

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"

	"monitor/internal/config"
	"monitor/internal/logger"
)

// Language 语言配置
type Language struct {
	Code        string // 完整语言码（zh-CN, en-US 等）
	PathPrefix  string // URL 路径前缀（'', en, ru, ja）
	HreflangTag string // hreflang 标签（zh-CN, en, ru, ja）
}

// 支持的语言列表（与前端 i18n/index.ts 保持一致）
var supportedLanguages = []Language{
	{Code: "zh-CN", PathPrefix: "", HreflangTag: "zh-CN"},
	{Code: "en-US", PathPrefix: "en", HreflangTag: "en"},
	{Code: "ru-RU", PathPrefix: "ru", HreflangTag: "ru"},
	{Code: "ja-JP", PathPrefix: "ja", HreflangTag: "ja"},
}

// 路径前缀到语言码的映射（与前端 PATH_LANGUAGE_MAP 对应）
var pathToLangCode = map[string]string{
	"":   "zh-CN",
	"en": "en-US",
	"ru": "ru-RU",
	"ja": "ja-JP",
}

// providerSlugRegex 用于校验 slug 格式（小写字母、数字、连字符）
var providerSlugRegex = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// isValidProviderSlug 校验 provider slug 是否合法
func isValidProviderSlug(slug string) bool {
	if slug == "" || len(slug) > 100 {
		return false
	}
	return providerSlugRegex.MatchString(slug)
}

// 允许 SEO 索引的静态页面路径（不含语言前缀部分）
var indexableStaticPaths = map[string]bool{
	"":        true, // 首页
	"contact": true,
	// 注：旧 /detect 专题页已下线，由 server.go 的 redirectLegacyDetect 301 到 rpdiag 站点。
}

// trimLanguagePrefix 去掉路径前后斜杠与语言前缀，返回剩余的路径段。
// 例：/en/contact → contact；/en/ → ""；/contact → contact；/p/foo → p/foo。
func trimLanguagePrefix(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return ""
	}

	parts := strings.SplitN(trimmed, "/", 2)
	if _, isLang := pathToLangCode[parts[0]]; isLang {
		if len(parts) == 1 {
			return "" // 仅语言前缀，等同首页
		}
		return parts[1]
	}

	return trimmed
}

// isValidHomePath 检查路径是否为有效的可索引页面路径（首页或白名单静态页）
func isValidHomePath(path string) bool {
	return indexableStaticPaths[trimLanguagePrefix(path)]
}

// parseStaticPath 解析白名单静态内容页的 key（不含语言前缀）；首页或非白名单返回 ""
func parseStaticPath(path string) string {
	staticPath := trimLanguagePrefix(path)
	if staticPath != "" && indexableStaticPaths[staticPath] {
		return staticPath
	}
	return ""
}

// MetaData 页面 Meta 数据
type MetaData struct {
	Title          string
	Description    string
	Language       Language
	Slug           string // URL slug（仅服务商页面，用于构造链接）
	ProviderName   string // 服务商显示名称（仅服务商页面，用于文案）
	IsProviderPage bool   // 是否为服务商页面
	StaticPath     string // 白名单静态内容页 key（不含语言前缀；空=首页或服务商页）
}

// PageMeta 生成的完整 meta 标签
type PageMeta struct {
	BasicMeta   string // title + description
	Canonical   string // canonical 标签
	Hreflang    string // hreflang 标签组
	OpenGraph   string // Open Graph 标签组
	TwitterCard string // Twitter Card 标签组
	JSONLD      string // JSON-LD 结构化数据
}

// parseRequestPath 解析请求路径，提取语言和 provider slug
func parseRequestPath(path string) (langCode string, providerSlug string, isProviderPage bool) {
	// 默认中文
	langCode = "zh-CN"

	// 移除前后斜杠
	path = strings.Trim(path, "/")

	if path == "" {
		return // 中文首页
	}

	parts := strings.Split(path, "/")

	// 检查第一部分是否为语言前缀
	if lang, exists := pathToLangCode[parts[0]]; exists {
		langCode = lang
		parts = parts[1:] // 移除语言前缀
	}

	// 检查是否为服务商页面 /p/:slug
	if len(parts) >= 2 && parts[0] == "p" {
		isProviderPage = true
		providerSlug = parts[1]
	}

	return
}

// getLanguageByCode 根据语言码获取 Language 对象
func getLanguageByCode(code string) Language {
	for _, lang := range supportedLanguages {
		if lang.Code == code {
			return lang
		}
	}
	return supportedLanguages[0] // 默认中文
}

// getMetaContent 根据语言和页面类型获取 meta 内容
func getMetaContent(langCode string, slug string, providerName string, isProviderPage bool, staticPath string) MetaData {
	lang := getLanguageByCode(langCode)

	var title, description string

	if isProviderPage {
		// 服务商页面 - 使用 HTML 转义防止 XSS
		escapedName := html.EscapeString(providerName)
		switch langCode {
		case "zh-CN":
			title = fmt.Sprintf("%s 服务可用性监测 - RelayPulse", escapedName)
			description = fmt.Sprintf("实时监测 %s 的 API 可用性、延迟和服务质量，查看历史稳定性数据和赞助链路状态。", escapedName)
		case "en-US":
			title = fmt.Sprintf("%s Service Availability Monitoring - RelayPulse", escapedName)
			description = fmt.Sprintf("Monitor %s API availability, latency, and service quality in real time. View historical stability data and sponsored route status.", escapedName)
		case "ru-RU":
			title = fmt.Sprintf("Мониторинг доступности сервиса %s - RelayPulse", escapedName)
			description = fmt.Sprintf("Мониторинг доступности API %s, задержки и качества обслуживания в реальном времени.", escapedName)
		case "ja-JP":
			title = fmt.Sprintf("%s サービス可用性監視 - RelayPulse", escapedName)
			description = fmt.Sprintf("%s の API 可用性、レイテンシ、サービス品質をリアルタイムで監視します。", escapedName)
		}
	} else if staticPath == "contact" {
		// 联系页：直接复用前端 ContactPage 的 contact.meta.* i18n 文案，
		// 保证服务端注入（爬虫初见）与客户端 Helmet 渲染的标题/描述一致。
		switch langCode {
		case "zh-CN":
			title = "联系我们 | RelayPulse"
			description = "申请收录、变更通道配置或提交反馈"
		case "en-US":
			title = "Contact Us | RelayPulse"
			description = "Apply for listing, request changes, or submit feedback"
		case "ru-RU":
			title = "Связаться с нами | RelayPulse"
			description = "Подать заявку на добавление, запросить изменения или оставить отзыв"
		case "ja-JP":
			title = "お問い合わせ | RelayPulse"
			description = "掲載申請、変更リクエスト、フィードバックの送信"
		}
	} else {
		// 首页
		switch langCode {
		case "zh-CN":
			title = "RelayPulse - 实时监测API中转服务可用性矩阵"
			description = "RelayPulse - 实时监测全球 LLM 中转服务的可用性、延迟与赞助链路，帮助开发者快速评估服务商质量，发现最稳定的 API 提供商。支持 Claude、GPT、Gemini 等主流模型接口的连通性监测。"
		case "en-US":
			title = "RelayPulse - Real-time availability matrix for API relay services"
			description = "RelayPulse - Real-time monitoring of LLM relay services worldwide for availability, latency, and sponsored routes, helping developers quickly evaluate provider quality and discover the most stable API providers. Supports connectivity checks for mainstream model APIs such as Claude, GPT and Gemini."
		case "ru-RU":
			title = "RelayPulse - Матрица мониторинга доступности API-ретрансляционных сервисов в реальном времени"
			description = "RelayPulse - Мониторинг в реальном времени доступности, задержки и спонсорских маршрутов глобальных LLM-ретрансляционных сервисов, помогающий разработчикам быстро оценивать качество провайдеров и находить самых стабильных API-поставщиков. Поддерживается проверка подключения к API популярных моделей, таких как Claude, GPT и Gemini."
		case "ja-JP":
			title = "RelayPulse - API中継サービスの可用性マトリクスをリアルタイム監視"
			description = "RelayPulse - 世界中のLLM中継サービスの可用性・レイテンシ・スポンサー経路をリアルタイムで監視。開発者がプロバイダの品質を素早く評価し、最も安定したAPIプロバイダを見つけられるよう支援します。Claude・GPT・Gemini など主要モデルのAPI接続性チェックに対応。"
		}
	}

	return MetaData{
		Title:          title,
		Description:    description,
		Language:       lang,
		Slug:           slug,
		ProviderName:   providerName,
		IsProviderPage: isProviderPage,
		StaticPath:     staticPath,
	}
}

// generatePageMeta 生成完整的 meta 标签
func generatePageMeta(meta MetaData, baseURL string) PageMeta {
	// 1. 基础 meta
	basicMeta := fmt.Sprintf(`    <title>%s</title>
    <meta name="description" content="%s">`,
		meta.Title,
		meta.Description)

	// 2. Canonical URL - 使用已验证的数据重构，避免 XSS
	var canonicalURL string
	if meta.IsProviderPage {
		// 服务商页面：使用已验证的 slug
		if meta.Language.PathPrefix == "" {
			canonicalURL = fmt.Sprintf("%s/p/%s", baseURL, meta.Slug)
		} else {
			canonicalURL = fmt.Sprintf("%s/%s/p/%s", baseURL, meta.Language.PathPrefix, meta.Slug)
		}
	} else if meta.StaticPath != "" {
		// 静态内容页：使用已验证的静态路径 key
		if meta.Language.PathPrefix == "" {
			canonicalURL = fmt.Sprintf("%s/%s", baseURL, meta.StaticPath)
		} else {
			canonicalURL = fmt.Sprintf("%s/%s/%s", baseURL, meta.Language.PathPrefix, meta.StaticPath)
		}
	} else {
		// 首页：使用语言前缀
		if meta.Language.PathPrefix == "" {
			canonicalURL = fmt.Sprintf("%s/", baseURL)
		} else {
			canonicalURL = fmt.Sprintf("%s/%s/", baseURL, meta.Language.PathPrefix)
		}
	}
	canonical := fmt.Sprintf(`    <link rel="canonical" href="%s">`, canonicalURL)

	// 3. Hreflang 标签
	var hreflangBuilder strings.Builder
	for _, lang := range supportedLanguages {
		var href string
		if meta.IsProviderPage {
			// 使用 slug 而非 ProviderName 构造 URL
			if lang.PathPrefix == "" {
				href = fmt.Sprintf("%s/p/%s", baseURL, meta.Slug)
			} else {
				href = fmt.Sprintf("%s/%s/p/%s", baseURL, lang.PathPrefix, meta.Slug)
			}
		} else if meta.StaticPath != "" {
			if lang.PathPrefix == "" {
				href = fmt.Sprintf("%s/%s", baseURL, meta.StaticPath)
			} else {
				href = fmt.Sprintf("%s/%s/%s", baseURL, lang.PathPrefix, meta.StaticPath)
			}
		} else {
			if lang.PathPrefix == "" {
				href = fmt.Sprintf("%s/", baseURL)
			} else {
				href = fmt.Sprintf("%s/%s/", baseURL, lang.PathPrefix)
			}
		}
		hreflangBuilder.WriteString(fmt.Sprintf(`    <link rel="alternate" hreflang="%s" href="%s">`+"\n", lang.HreflangTag, href))
	}

	// x-default 指向中文版本
	if meta.IsProviderPage {
		hreflangBuilder.WriteString(fmt.Sprintf(`    <link rel="alternate" hreflang="x-default" href="%s/p/%s">`, baseURL, meta.Slug))
	} else if meta.StaticPath != "" {
		hreflangBuilder.WriteString(fmt.Sprintf(`    <link rel="alternate" hreflang="x-default" href="%s/%s">`, baseURL, meta.StaticPath))
	} else {
		hreflangBuilder.WriteString(fmt.Sprintf(`    <link rel="alternate" hreflang="x-default" href="%s/">`, baseURL))
	}

	// 4. Open Graph
	ogType := "website"
	ogImage := baseURL + "/og-image.png" // 可以后续添加实际图片
	openGraph := fmt.Sprintf(`    <meta property="og:type" content="%s">
    <meta property="og:title" content="%s">
    <meta property="og:description" content="%s">
    <meta property="og:url" content="%s">
    <meta property="og:image" content="%s">
    <meta property="og:locale" content="%s">`,
		ogType,
		meta.Title,
		meta.Description,
		canonicalURL,
		ogImage,
		strings.Replace(meta.Language.Code, "-", "_", 1)) // zh-CN → zh_CN

	// 5. Twitter Card
	twitterCard := fmt.Sprintf(`    <meta name="twitter:card" content="summary_large_image">
    <meta name="twitter:title" content="%s">
    <meta name="twitter:description" content="%s">
    <meta name="twitter:image" content="%s">`,
		meta.Title,
		meta.Description,
		ogImage)

	// 6. JSON-LD 结构化数据
	var jsonLD string
	if meta.IsProviderPage {
		// 服务商页面：Service 类型
		jsonLDData := map[string]interface{}{
			"@context": "https://schema.org",
			"@type":    "Service",
			"name":     fmt.Sprintf("%s API 监测", meta.ProviderName),
			"provider": map[string]interface{}{
				"@type": "Organization",
				"name":  meta.ProviderName,
			},
			"areaServed": "全球",
		}
		jsonLDBytes, err := json.MarshalIndent(jsonLDData, "    ", "  ")
		if err != nil {
			logger.Warn("seo", "JSON-LD 序列化失败", "provider", meta.Slug, "error", err)
			jsonLD = ""
		} else {
			jsonLD = fmt.Sprintf(`    <script type="application/ld+json">
    %s
    </script>`, string(jsonLDBytes))
		}
	} else if meta.StaticPath == "contact" {
		// 联系页：ContactPage 类型（首页才用 WebSite）
		jsonLDData := map[string]interface{}{
			"@context":    "https://schema.org",
			"@type":       "ContactPage",
			"name":        meta.Title,
			"url":         canonicalURL,
			"description": meta.Description,
			"inLanguage":  meta.Language.Code,
		}
		jsonLDBytes, err := json.MarshalIndent(jsonLDData, "    ", "  ")
		if err != nil {
			logger.Warn("seo", "JSON-LD 序列化失败", "staticPath", meta.StaticPath, "error", err)
			jsonLD = ""
		} else {
			jsonLD = fmt.Sprintf(`    <script type="application/ld+json">
    %s
    </script>`, string(jsonLDBytes))
		}
	} else {
		// 首页：WebSite 类型
		jsonLDData := map[string]interface{}{
			"@context":    "https://schema.org",
			"@type":       "WebSite",
			"name":        "RelayPulse",
			"url":         baseURL,
			"description": meta.Description,
			"inLanguage":  []string{"zh-CN", "en-US", "ru-RU", "ja-JP"},
		}
		jsonLDBytes, err := json.MarshalIndent(jsonLDData, "    ", "  ")
		if err != nil {
			logger.Warn("seo", "JSON-LD 序列化失败", "lang", meta.Language.Code, "error", err)
			jsonLD = ""
		} else {
			jsonLD = fmt.Sprintf(`    <script type="application/ld+json">
    %s
    </script>`, string(jsonLDBytes))
		}
	}

	return PageMeta{
		BasicMeta:   basicMeta,
		Canonical:   canonical,
		Hreflang:    hreflangBuilder.String(),
		OpenGraph:   openGraph,
		TwitterCard: twitterCard,
		JSONLD:      jsonLD,
	}
}

// injectMetaTags 在 index.html 中注入 meta 标签
// 返回 (html, isNotFound)，isNotFound 表示 provider 不存在
func injectMetaTags(indexHTML string, path string, cfg *config.AppConfig) (string, bool) {
	baseURL := cfg.PublicBaseURL

	// 解析路径
	langCode, providerSlug, isProviderPage := parseRequestPath(path)

	// 如果是服务商页面，进行 slug 校验和存在性检查
	providerName := ""
	providerExists := false

	if isProviderPage {
		// 1. 校验 slug 格式（防止 XSS）
		if !isValidProviderSlug(providerSlug) {
			// slug 格式非法，返回 404
			return inject404Meta(indexHTML, langCode), true
		}

		// 2. 从配置中查找 provider
		if cfg != nil {
			for _, monitor := range cfg.Monitors {
				slug := monitor.ProviderSlug
				if slug == "" {
					slug = strings.ToLower(strings.TrimSpace(monitor.Provider))
				}
				if slug == providerSlug {
					providerName = monitor.Provider
					providerExists = true
					break
				}
			}
		}

		// 3. provider 不存在，返回 404
		if !providerExists {
			return inject404Meta(indexHTML, langCode), true
		}
	}

	// 非服务商页面：检查是否为有效首页
	// 有效首页：/、/en/、/ru/、/ja/
	// 无效路径：/foo、/foo/bar、/en/foo 等，注入 noindex 防止收录
	if !isProviderPage && !isValidHomePath(path) {
		return injectNoindexMeta(indexHTML, langCode), false
	}

	// 静态内容页（如 /contact）走专属文案与 canonical；首页/服务商页 staticPath 为空
	staticPath := ""
	if !isProviderPage {
		staticPath = parseStaticPath(path)
	}

	// 获取 meta 内容（传入 slug、displayName 与静态页 key）
	metaData := getMetaContent(langCode, providerSlug, providerName, isProviderPage, staticPath)

	// 生成完整 meta 标签
	pageMeta := generatePageMeta(metaData, baseURL)

	// 替换原有的 title 和 description
	html := indexHTML

	// 替换 <html lang="...">
	html = replaceHtmlLang(html, metaData.Language.Code)

	// 替换 <title>...</title>
	html = replaceBetween(html, "<title>", "</title>", metaData.Title)

	// 替换 <meta name="description" ...>
	html = replaceMetaDescription(html, metaData.Description)

	// 在 </head> 前插入其他 meta 标签
	additionalMeta := fmt.Sprintf("\n%s\n%s\n%s\n%s\n%s\n",
		pageMeta.Canonical,
		pageMeta.Hreflang,
		pageMeta.OpenGraph,
		pageMeta.TwitterCard,
		pageMeta.JSONLD)

	html = strings.Replace(html, "</head>", additionalMeta+"  </head>", 1)

	return html, false
}

// inject404Meta 注入 404 页面的 meta 标签（noindex）
func inject404Meta(indexHTML string, langCode string) string {
	var title, description string
	switch langCode {
	case "zh-CN":
		title = "页面未找到 - RelayPulse"
		description = "您访问的服务商页面不存在"
	case "en-US":
		title = "Page Not Found - RelayPulse"
		description = "The provider page you are looking for does not exist"
	case "ru-RU":
		title = "Страница не найдена - RelayPulse"
		description = "Страница провайдера, которую вы ищете, не существует"
	case "ja-JP":
		title = "ページが見つかりません - RelayPulse"
		description = "お探しのプロバイダーページは存在しません"
	default:
		title = "Page Not Found - RelayPulse"
		description = "The provider page you are looking for does not exist"
	}

	htmlContent := indexHTML

	// 替换 lang 属性
	htmlContent = replaceHtmlLang(htmlContent, langCode)

	htmlContent = replaceBetween(htmlContent, "<title>", "</title>", html.EscapeString(title))
	htmlContent = replaceMetaDescription(htmlContent, html.EscapeString(description))

	// 添加 noindex meta 标签
	noindexMeta := `    <meta name="robots" content="noindex, nofollow">`
	htmlContent = strings.Replace(htmlContent, "</head>", "\n"+noindexMeta+"\n  </head>", 1)

	return htmlContent
}

// injectNoindexMeta 注入 noindex meta 标签（用于非白名单路径，保持首页内容）
func injectNoindexMeta(indexHTML string, langCode string) string {
	// 获取首页的 meta 内容（noindex 分支固定走首页文案，不识别静态页）
	metaData := getMetaContent(langCode, "", "", false, "")

	htmlContent := indexHTML

	// 替换 lang 属性
	htmlContent = replaceHtmlLang(htmlContent, langCode)

	// 替换 title 和 description
	htmlContent = replaceBetween(htmlContent, "<title>", "</title>", metaData.Title)
	htmlContent = replaceMetaDescription(htmlContent, metaData.Description)

	// 添加 noindex meta 标签
	noindexMeta := `    <meta name="robots" content="noindex, nofollow">`
	htmlContent = strings.Replace(htmlContent, "</head>", "\n"+noindexMeta+"\n  </head>", 1)

	return htmlContent
}

// replaceBetween 替换两个标记之间的内容
func replaceBetween(s, start, end, newContent string) string {
	startIdx := strings.Index(s, start)
	if startIdx == -1 {
		return s
	}
	startIdx += len(start)

	endIdx := strings.Index(s[startIdx:], end)
	if endIdx == -1 {
		return s
	}
	endIdx += startIdx

	return s[:startIdx] + newContent + s[endIdx:]
}

// replaceMetaDescription 替换 meta description 标签
func replaceMetaDescription(html, newDescription string) string {
	// 匹配 <meta name="description" content="...">
	start := `<meta name="description" content="`
	startIdx := strings.Index(html, start)
	if startIdx == -1 {
		return html
	}
	startIdx += len(start)

	endIdx := strings.Index(html[startIdx:], `"`)
	if endIdx == -1 {
		return html
	}
	endIdx += startIdx

	return html[:startIdx] + newDescription + html[endIdx:]
}

// replaceHtmlLang 替换 <html lang="..."> 中的语言属性
func replaceHtmlLang(html, newLang string) string {
	// 匹配 <html lang="...">
	start := `<html lang="`
	startIdx := strings.Index(html, start)
	if startIdx == -1 {
		return html
	}
	startIdx += len(start)

	endIdx := strings.Index(html[startIdx:], `"`)
	if endIdx == -1 {
		return html
	}
	endIdx += startIdx

	return html[:startIdx] + newLang + html[endIdx:]
}
