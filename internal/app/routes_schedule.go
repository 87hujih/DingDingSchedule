package app

import (
	"schedule_server/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerScheduleRoutes 注册课表相关路由（需鉴权）
func registerScheduleRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	schedules := rg.Group("/schedules")
	{
		schedules.POST("/import", h.ScheduleHdl.Import)
		schedules.GET("", h.ScheduleHdl.List)          // 按周查询课表
		schedules.GET("/all", h.ScheduleHdl.ListAll)   // 获取全部课程（用于管理）
		schedules.POST("", h.ScheduleHdl.Create)       // 手动添加课程
		schedules.PUT("/:id", h.ScheduleHdl.Update)    // 更新课程
		schedules.DELETE("/:id", h.ScheduleHdl.Delete) // 删除课程
	}
}
