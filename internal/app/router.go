package app

import (
	"net/http"

	"schedule_server/global"
	"schedule_server/internal/handler"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// setupRouter 初始化路由引擎和依赖注入
func setupRouter() *gin.Engine {
	gin.SetMode(global.AppConfig.Server.Mode)

	r := gin.Default()

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// 依赖注入：Repository → Service → Handler
	repo := repository.NewRepository(global.DB)
	svc := service.NewService(repo)
	h := handler.NewHandler(svc)

	// 注册业务路由
	registerRoutes(r, h)

	return r
}

// registerRoutes 注册所有业务路由
func registerRoutes(r *gin.Engine, h *handler.Handler) {
	// TODO: 在此注册具体路由
	// api := r.Group("/api/v1")
	// {
	//     api.GET("/users", h.UserHandler.List)
	//     api.POST("/users", h.UserHandler.Create)
	// }
	_ = h // 暂时忽略未使用警告
}
