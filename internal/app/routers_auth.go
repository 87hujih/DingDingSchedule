package app

import (
	"schedule_server/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerAuthRoutes 注册认证相关路由（公开）
func registerAuthRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	auth := rg.Group("/auth")
	{
		auth.POST("/login", h.AuthHdl.Login) // 登录
	}
}
