package app

import (
	"schedule_server/internal/handler"
	"schedule_server/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerAdminRoutes 注册管理员相关路由（需鉴权 + 实验室管理员权限）
func registerAdminRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	admin := rg.Group("/admin", middleware.RequireLabAdmin())
	{
		// 部门同步
		admin.POST("/departments/sync", h.DeptHdl.Sync)

		// 学期管理
		semesters := admin.Group("/semesters")
		{
			semesters.GET("/list", h.SemesterHdl.List)
			semesters.POST("/create", h.SemesterHdl.Create)
			semesters.PUT("/update/:id", h.SemesterHdl.Update)
			semesters.DELETE("/delete/:id", h.SemesterHdl.Delete)
		}
	}
}
