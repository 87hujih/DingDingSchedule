package app

import (
	"schedule_server/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerAdminRoutes 注册管理员相关路由（需鉴权）
func registerAdminRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	admin := rg.Group("/admin")
	{
		admin.POST("/departments/sync", h.DeptHdl.Sync)
	}
}
