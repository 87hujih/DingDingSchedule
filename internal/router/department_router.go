package router

import (
	"schedule_server/internal/handler"

	"github.com/gin-gonic/gin"
)

// DepartmentRouter 部门相关路由
func DepartmentRouter(r *gin.Engine, h *handler.Handler) {
	admin := r.Group("/api/admin")
	{
		admin.POST("/departments/sync", h.DeptHdl.Sync)
	}
}
