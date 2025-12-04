package app

import (
	"schedule_server/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerUserRoutes 注册用户相关路由（需鉴权）
func registerUserRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	// 用户管理
	users := rg.Group("/users")
	{
		users.GET("/me", h.UserHdl.GetCurrentUser)
		users.GET("", h.UserHdl.List)
		users.POST("", h.UserHdl.Create)
	}
}
