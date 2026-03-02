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
		users.GET("/me", h.UserHdl.GetCurrentUser) // 获取个人信息
		users.POST("/refresh", h.UserHdl.Refresh)  // 刷新个人信息
		users.GET("/:id", h.UserHdl.GetByID)       // 查看用户个人信息
	}
	search := rg.Group("/search")
	{
		// 普通用户搜索 (用于导入他人课表)
		search.GET("", h.UserHdl.List)
	}
	// 部门列表（普通用户可见，用于筛选）
	departments := rg.Group("/departments")
	{
		departments.GET("", h.DeptHdl.List)
	}
}
