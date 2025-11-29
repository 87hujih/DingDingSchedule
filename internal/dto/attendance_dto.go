package dto

import "schedule_server/internal/model"

// ===================== 请求 DTO =====================

// CheckInReq 签到请求
type CheckInReq struct {
	WiFiMAC string `json:"wifi_mac" binding:"required"`
}

// CheckOutReq 签退请求
type CheckOutReq struct {
	WiFiMAC string `json:"wifi_mac" binding:"required"`
}

// AttendanceQueryReq 考勤查询请求
type AttendanceQueryReq struct {
	UserID    uint   `json:"user_id" form:"user_id"`
	StartDate string `json:"start_date" form:"start_date"`
	EndDate   string `json:"end_date" form:"end_date"`
	Page      int    `json:"page" form:"page,default=1"`
	PageSize  int    `json:"page_size" form:"page_size,default=10"`
}

// ===================== 响应 DTO =====================

// AttendanceDTO 考勤响应
type AttendanceDTO struct {
	ID         uint          `json:"id"`
	CheckInAt  string        `json:"check_in_at,omitempty"`
	CheckOutAt string        `json:"check_out_at,omitempty"`
	Status     int           `json:"status"`
	StatusText string        `json:"status_text"`
	User       *UserBriefDTO `json:"user,omitempty"`
}

// AttendanceStatDTO 考勤统计
type AttendanceStatDTO struct {
	TotalDays  int `json:"total_days"`  // 应出勤天数
	ActualDays int `json:"actual_days"` // 实际出勤天数
	LateDays   int `json:"late_days"`   // 迟到天数
	EarlyDays  int `json:"early_days"`  // 早退天数
	AbsentDays int `json:"absent_days"` // 缺勤天数
}

// ===================== 转换函数 =====================

// ToAttendanceDTO model → DTO
func ToAttendanceDTO(a *model.Attendance) *AttendanceDTO {
	dto := &AttendanceDTO{
		ID:         a.ID,
		Status:     a.Status,
		StatusText: getAttendanceStatusText(a.Status),
	}
	if !a.CheckInAt.IsZero() {
		dto.CheckInAt = a.CheckInAt.Format("2006-01-02 15:04:05")
	}
	if !a.CheckOutAt.IsZero() {
		dto.CheckOutAt = a.CheckOutAt.Format("2006-01-02 15:04:05")
	}
	if a.User != nil {
		dto.User = ToUserBriefDTO(a.User)
	}
	return dto
}

func getAttendanceStatusText(status int) string {
	switch status {
	case 0:
		return "未签到"
	case 1:
		return "已签到"
	case 2:
		return "已签退"
	case 3:
		return "迟到"
	case 4:
		return "早退"
	case 5:
		return "缺勤"
	default:
		return "未知"
	}
}
