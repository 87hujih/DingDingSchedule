package handler

import (
	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// ScheduleSettingHandler 作息设置处理器
type ScheduleSettingHandler struct {
	schedulePeriodSrv *service.SchedulePeriodService
}

func NewScheduleSettingHandler(srv *service.SchedulePeriodService) *ScheduleSettingHandler {
	return &ScheduleSettingHandler{schedulePeriodSrv: srv}
}

// GetScheduleInfo 获取作息配置信息
// GET /api/schedule/info
func (h *ScheduleSettingHandler) GetScheduleInfo(c *gin.Context) {
	info, err := h.schedulePeriodSrv.GetScheduleInfo(c.Request.Context())
	if err != nil {
		response.FailWithError(c, err)
		return
	}
	response.OK(c, info)
}

// SwitchMode 切换作息模式
// POST /api/schedule/switch-mode
func (h *ScheduleSettingHandler) SwitchMode(c *gin.Context) {
	var req dto.SwitchModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeInvalidParam, "请求参数错误")
		return
	}

	if err := h.schedulePeriodSrv.SwitchMode(c.Request.Context(), req.Mode); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OK(c, gin.H{"message": "切换成功", "current_mode": req.Mode})
}

// GetCurrentMode 获取当前作息模式
// GET /api/schedule/current-mode
func (h *ScheduleSettingHandler) GetCurrentMode(c *gin.Context) {
	mode, err := h.schedulePeriodSrv.GetCurrentMode(c.Request.Context())
	if err != nil {
		response.FailWithError(c, err)
		return
	}
	response.OK(c, gin.H{"current_mode": mode})
}

// ToggleAttendance 切换考勤开关
// POST /api/schedule/attendance/toggle
func (h *ScheduleSettingHandler) ToggleAttendance(c *gin.Context) {
	var req dto.ToggleAttendanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeInvalidParam, "请求参数错误")
		return
	}

	if err := h.schedulePeriodSrv.SetAttendanceEnabled(c.Request.Context(), req.Enabled); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OK(c, gin.H{"enabled": req.Enabled})
}

// GetAttendanceStatus 获取考勤开关状态
// GET /api/schedule/attendance/status
func (h *ScheduleSettingHandler) GetAttendanceStatus(c *gin.Context) {
	enabled, err := h.schedulePeriodSrv.IsAttendanceEnabled(c.Request.Context())
	if err != nil {
		response.FailWithError(c, err)
		return
	}
	response.OK(c, dto.AttendanceStatusResponse{Enabled: enabled})
}

// ToggleScheduleChangeNotify 切换课表变更通知开关
// POST /api/schedule/notify/schedule-change/toggle
func (h *ScheduleSettingHandler) ToggleScheduleChangeNotify(c *gin.Context) {
	var req dto.ToggleScheduleChangeNotifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeInvalidParam, "请求参数错误")
		return
	}

	if err := h.schedulePeriodSrv.SetScheduleChangeNotifyEnabled(c.Request.Context(), req.Enabled); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OK(c, gin.H{"enabled": req.Enabled})
}

// ToggleLateNotify 切换迟到提醒通知开关
// POST /api/schedule/notify/late-reminder/toggle
func (h *ScheduleSettingHandler) ToggleLateNotify(c *gin.Context) {
	var req dto.ToggleLateNotifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeInvalidParam, "请求参数错误")
		return
	}

	if err := h.schedulePeriodSrv.SetLateNotifyEnabled(c.Request.Context(), req.Enabled); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OK(c, gin.H{"enabled": req.Enabled})
}

// GetNotifyStatus 获取通知开关状态
// GET /api/schedule/notify/status
func (h *ScheduleSettingHandler) GetNotifyStatus(c *gin.Context) {
	scheduleChangeEnabled, err := h.schedulePeriodSrv.IsScheduleChangeNotifyEnabled(c.Request.Context())
	if err != nil {
		response.FailWithError(c, err)
		return
	}
	lateEnabled, err := h.schedulePeriodSrv.IsLateNotifyEnabled(c.Request.Context())
	if err != nil {
		response.FailWithError(c, err)
		return
	}
	response.OK(c, dto.NotifyStatusResponse{
		ScheduleChangeNotifyEnabled: scheduleChangeEnabled,
		LateNotifyEnabled:           lateEnabled,
	})
}
