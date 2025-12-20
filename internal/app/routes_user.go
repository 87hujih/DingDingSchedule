package app

import (
	"schedule_server/internal/handler"
	"schedule_server/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerUserRoutes 注册用户相关路由（需鉴权）
func registerUserRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	// 用户管理
	users := rg.Group("/users")
	{
		users.GET("/me", h.UserHdl.GetCurrentUser)
		users.GET("/search", h.UserHdl.List)                                               // 用户搜索（鉴权）
		users.GET("/search_visible", middleware.RequireGroupLead(), h.UserHdl.ListVisible) // 非普通角色的可见范围搜索
		users.GET("/:id", middleware.RequireGroupLead(), h.UserHdl.GetUser)                // 查询其他用户（需非普通角色）
	}
}
