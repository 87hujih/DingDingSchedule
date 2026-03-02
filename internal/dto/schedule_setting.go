package dto

// SwitchModeRequest 切换模式请求
type SwitchModeRequest struct {
	Mode string `json:"mode" binding:"required,oneof=school holiday"` // school 或 holiday
}

// ToggleAttendanceRequest 切换考勤开关请求
type ToggleAttendanceRequest struct {
	Enabled bool `json:"enabled"` // true=开启, false=关闭
}

// AttendanceStatusResponse 考勤状态响应
type AttendanceStatusResponse struct {
	Enabled bool `json:"enabled"`
}

// ToggleScheduleChangeNotifyRequest 切换课表变更通知开关请求
type ToggleScheduleChangeNotifyRequest struct {
	Enabled bool `json:"enabled"`
}

// ToggleLateNotifyRequest 切换迟到提醒通知开关请求
type ToggleLateNotifyRequest struct {
	Enabled bool `json:"enabled"`
}

// NotifyStatusResponse 通知开关状态响应
type NotifyStatusResponse struct {
	ScheduleChangeNotifyEnabled bool `json:"schedule_change_notify_enabled"`
	LateNotifyEnabled           bool `json:"late_notify_enabled"`
}
