package app

import (
	"schedule_server/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerAuditLogRoutes 注册审计日志相关路由（需管理员权限，已在 admin 组内）
func registerAuditLogRoutes(admin *gin.RouterGroup, h *handler.Handler) {
	admin.GET("/audit-logs", h.AuditLogHdl.List)
}
