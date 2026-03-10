package app

import (
	"schedule_server/internal/handler"
	"schedule_server/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerRestDayRoutes 用户休息日相关路由
func registerRestDayRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	restDay := rg.Group("/rest-day")
	{
		// 用户自己的休息日
		restDay.GET("/mine", h.RestDayHdl.GetMyRestDay)
		restDay.POST("/mine", h.RestDayHdl.SetMyRestDay)

		// 管理员查看所有用户休息日
		restDay.GET("/users", middleware.RequireAdmin(), h.RestDayHdl.ListAllRestDays)
	}
}
