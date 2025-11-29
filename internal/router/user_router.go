package router

import (
	"schedule_server/internal/handler"

	"github.com/gin-gonic/gin"
)

// UserRouter 注册用户相关路由
func UserRouter(r *gin.Engine, h *handler.Handler) {
	user := r.Group("/api/v1/users")
	{
		user.GET("", h.UserHdl.List)
		user.POST("", h.UserHdl.Create)
	}
}
