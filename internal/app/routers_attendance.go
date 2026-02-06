package app

import (
	"schedule_server/internal/handler"
	"schedule_server/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerAttendanceRoutes 注册考勤相关路由（需鉴权）
func registerAttendanceRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	attendance := rg.Group("/attendance")
	{
		attendance.GET("/slots/status", h.AttendanceHdl.SlotAttendanceStatus)                                         // 时段考勤状态
		attendance.GET("/slots/users/:user_id/leave", middleware.RequireAdmin(), h.AttendanceHdl.SlotUserLeaveDetail) // 时段请假明细
		// 考勤统计记录（管理员）
		record := attendance.Group("/record")
		{
			record.GET("/ranking/weekly", h.AttendanceRecordHdl.GetWeeklyRanking)      // 本周考勤排行
			record.GET("/detail", h.AttendanceRecordHdl.GetAttendanceDetail)           // 获取考勤详情（实时计算）
			record.GET("/snapshot", h.AttendanceRecordHdl.GetAttendanceSnapshot)       // 获取考勤快照（已保存记录）
			record.POST("/trigger", h.AttendanceRecordHdl.TriggerAttendanceStatistics) // 手动触发统计
			record.GET("/list", h.AttendanceRecordHdl.GetAttendanceRecords)            // 获取某天所有记录
			record.GET("/text", h.AttendanceRecordHdl.GetAttendanceText)               // 获取考勤文本（用于复制到群里）
			record.POST("/sign", h.AttendanceRecordHdl.SignForUser)                    // 代签（补签）

		}
	}
}
