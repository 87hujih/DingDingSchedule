package app

import (
	"schedule_server/internal/handler"
	"schedule_server/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerAdminRoutes 注册管理员相关路由（需鉴权 + 管理员权限）
func registerAdminRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	admin := rg.Group("/admin", middleware.RequireAdmin())
	{
		// 部门管理
		departments := admin.Group("/departments")
		{
			departments.GET("", h.DeptHdl.List)                      // 获取所有部门信息
			departments.POST("/sync", h.DeptHdl.Sync)                // 同步所有部门信息
			departments.PATCH("/:id/status", h.DeptHdl.UpdateStatus) // 更新部门考勤状态
			departments.DELETE("/:id", h.DeptHdl.Delete)             // 删除部门（软删除）
		}

		// 用户管理
		users := admin.Group("/users")
		{
			users.POST("/sync_all", h.UserHdl.SyncAll)         // 刷新本地所有用户的个人钉钉信息
			users.PATCH("/:id/status", h.UserHdl.UpdateStatus) // 更新用户考勤状态
			users.DELETE("/:id", h.UserHdl.Delete)             // 删除用户（软删除）
		}
	}
}
