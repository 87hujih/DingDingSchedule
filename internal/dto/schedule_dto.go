package dto

import (
	"schedule_server/internal/model"
	"time"
)

// ===================== 请求 DTO =====================

// ScheduleCreateReq 创建排班请求
type ScheduleCreateReq struct {
	UserID    uint   `json:"user_id" binding:"required"`
	Date      string `json:"date" binding:"required"`       // 格式: 2006-01-02
	StartTime string `json:"start_time" binding:"required"` // 格式: 15:04
	EndTime   string `json:"end_time" binding:"required"`   // 格式: 15:04
}

// ScheduleQueryReq 查询排班请求
type ScheduleQueryReq struct {
	UserID    uint   `json:"user_id" form:"user_id"`
	StartDate string `json:"start_date" form:"start_date"`
	EndDate   string `json:"end_date" form:"end_date"`
	Page      int    `json:"page" form:"page,default=1"`
	PageSize  int    `json:"page_size" form:"page_size,default=10"`
}

// ===================== 响应 DTO =====================

// ScheduleDTO 排班响应
type ScheduleDTO struct {
	ID         uint          `json:"id"`
	Date       string        `json:"date"`
	StartTime  string        `json:"start_time"`
	EndTime    string        `json:"end_time"`
	Status     int           `json:"status"`
	StatusText string        `json:"status_text"`
	User       *UserBriefDTO `json:"user,omitempty"`
}

// ===================== 转换函数 =====================

// ToScheduleDTO model → DTO
func ToScheduleDTO(s *model.Schedule) *ScheduleDTO {
	dto := &ScheduleDTO{
		ID:         s.ID,
		Date:       s.Date.Format("2006-01-02"),
		StartTime:  s.StartTime.Format("15:04"),
		EndTime:    s.EndTime.Format("15:04"),
		Status:     s.Status,
		StatusText: getScheduleStatusText(s.Status),
	}
	// 如果有关联用户
	if s.User != nil {
		dto.User = ToUserBriefDTO(s.User)
	}
	return dto
}

func getScheduleStatusText(status int) string {
	switch status {
	case 0:
		return "待签到"
	case 1:
		return "已签到"
	case 2:
		return "已签退"
	case 3:
		return "缺勤"
	default:
		return "未知"
	}
}

// ParseDate 解析日期字符串
func ParseDate(dateStr string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", dateStr, time.Local)
}

// ParseTime 解析时间字符串（需要日期作为基准）
func ParseTime(date time.Time, timeStr string) (time.Time, error) {
	t, err := time.ParseInLocation("15:04", timeStr, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(date.Year(), date.Month(), date.Day(), t.Hour(), t.Minute(), 0, 0, time.Local), nil
}
