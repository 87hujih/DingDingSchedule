package app

import (
	"schedule_server/global"
	"schedule_server/internal/handler"
	"schedule_server/internal/middleware"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// setupRouter 初始化路由引擎和依赖注入
func setupRouter() *gin.Engine {
	gin.SetMode(global.AppConfig.Server.Mode)

	// 初始化中间件
	middleware.Init(global.AppConfig.JWT)

	// 依赖注入
	repo := repository.NewRepository(global.DB)
	svc := service.NewService(repo, global.DingTalk, global.AppConfig.JWT)
	h := handler.NewHandler(svc)

	// 注册路由
	r := gin.Default()
	registerRoutes(r, h)

	return r
}

// registerRoutes 注册所有路由
func registerRoutes(r *gin.Engine, h *handler.Handler) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 公开路由（无需鉴权）
	public := r.Group("/api")
	registerAuthRoutes(public, h)

	// 需要鉴权的路由
	protected := r.Group("/api", middleware.JWTAuth())
	registerUserRoutes(protected, h)
	registerAdminRoutes(protected, h)
	registerScheduleRoutes(protected, h)
}
