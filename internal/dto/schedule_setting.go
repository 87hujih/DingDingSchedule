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

// ToggleRestDayEditingRequest 切换休息日编辑权限请求
type ToggleRestDayEditingRequest struct {
	Allowed bool `json:"allowed"` // true=允许编辑, false=禁止编辑
}

// RestDayEditingStatusResponse 休息日编辑权限状态响应
type RestDayEditingStatusResponse struct {
	Allowed bool `json:"allowed"`
}
