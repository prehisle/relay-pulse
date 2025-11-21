package api

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/storage"
)

//go:embed frontend/dist
var frontendFS embed.FS

var (
	// 版本信息（通过 ldflags 注入）
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

// 版本信息获取函数
func getVersion() string   { return Version }
func getGitCommit() string { return GitCommit }
func getBuildTime() string { return BuildTime }

// Server HTTP服务器
type Server struct {
	handler    *Handler
	router     *gin.Engine
	httpServer *http.Server
	port       string
}

// NewServer 创建服务器
func NewServer(store storage.Storage, cfg *config.AppConfig, port string) *Server {
	// 设置gin模式
	gin.SetMode(gin.ReleaseMode)

	// 创建路由
	router := gin.Default()

	// CORS中间件 - 从环境变量获取允许的来源
	allowedOrigins := []string{"https://relaypulse.top"}
	if extraOrigins := os.Getenv("MONITOR_CORS_ORIGINS"); extraOrigins != "" {
		// 支持逗号分隔的多个域名，例如: MONITOR_CORS_ORIGINS=http://localhost:5173,http://localhost:3000
		allowedOrigins = append(allowedOrigins, strings.Split(extraOrigins, ",")...)
	}

	corsConfig := cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}
	router.Use(cors.New(corsConfig))

	// 创建处理器
	handler := NewHandler(store, cfg)

	// 注册 API 路由
	router.GET("/api/status", handler.GetStatus)

	// 版本信息 API
	router.GET("/api/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"version":    getVersion(),
			"git_commit": getGitCommit(),
			"build_time": getBuildTime(),
		})
	})

	// 健康检查
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// 静态文件服务（前端）
	setupStaticFiles(router)

	return &Server{
		handler: handler,
		router:  router,
		port:    port,
	}
}

// Start 启动服务器
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:         ":" + s.port,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("\n🚀 监控服务已启动")
	log.Printf("👉 Web 界面: http://localhost:%s", s.port)
	log.Printf("👉 API 地址: http://localhost:%s/api/status", s.port)
	log.Printf("👉 健康检查: http://localhost:%s/health\n", s.port)

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("启动HTTP服务失败: %w", err)
	}

	return nil
}

// Stop 停止服务器
func (s *Server) Stop(ctx context.Context) error {
	log.Println("[API] 正在关闭HTTP服务器...")

	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}

	return nil
}

// UpdateConfig 更新配置（热更新时调用）
func (s *Server) UpdateConfig(cfg *config.AppConfig) {
	s.handler.UpdateConfig(cfg)
}

// setupStaticFiles 设置静态文件服务（前端）
func setupStaticFiles(router *gin.Engine) {
	// 获取嵌入的前端文件系统
	distFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		log.Printf("[API] 警告: 无法加载前端文件系统: %v", err)
		return
	}

	// 获取 assets 子目录文件系统
	// StaticFS("/assets", ...) 会将 /assets/file.js 映射到文件系统根目录的 file.js
	// 所以需要创建一个子文件系统指向 assets 目录
	assetsFS, err := fs.Sub(distFS, "assets")
	if err != nil {
		log.Printf("[API] 警告: 无法加载 assets 文件系统: %v", err)
		return
	}

	// 静态资源路径（CSS、JS等）
	router.StaticFS("/assets", http.FS(assetsFS))

	// vite.svg 等根目录静态文件
	router.GET("/vite.svg", func(c *gin.Context) {
		data, err := fs.ReadFile(distFS, "vite.svg")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "image/svg+xml", data)
	})

	// SPA 路由回退 - 所有未匹配的路由返回 index.html
	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// API 路径返回 404
		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "API endpoint not found"})
			return
		}

		// 静态资源缺失直接返回 404，避免 SPA 回退导致 MIME 类型错误
		// 当 /assets/ 下的文件不存在时，StaticFS 不处理，请求会落入 NoRoute
		// 如果回退到 index.html，浏览器会因为 MIME 类型是 text/html 而报错
		if strings.HasPrefix(path, "/assets/") {
			c.Status(http.StatusNotFound)
			return
		}

		// 其他路径返回前端 index.html（SPA 路由）
		data, err := fs.ReadFile(distFS, "index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "Failed to load frontend")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})
}
