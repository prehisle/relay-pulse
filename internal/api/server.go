package api

import (
	"context"
	"fmt"
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

	// 注册路由
	router.GET("/api/status", handler.GetStatus)

	// 健康检查
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

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
