package model

import "time"

// ScheduleSetting 作息配置设置
type ScheduleSetting struct {
	ID                          uint      `gorm:"primaryKey" json:"id"`
	TenantID                    uint      `gorm:"not null;uniqueIndex:uniq_tenant" json:"tenant_id"`
	CurrentMode                 string    `gorm:"size:20;not null;default:school" json:"current_mode"`         // school/holiday
	SchoolSeason                string    `gorm:"size:10;not null;default:winter" json:"school_season"`         // summer/winter，上学模式下的季节
	AttendanceEnabled           bool      `gorm:"not null;default:true" json:"attendance_enabled"`             // 考勤总开关
	ScheduleChangeNotifyEnabled bool      `gorm:"not null;default:true" json:"schedule_change_notify_enabled"` // 课表变更通知开关
	LateNotifyEnabled           bool      `gorm:"not null;default:true" json:"late_notify_enabled"`            // 迟到提醒通知开关
	RestDayEditingAllowed       bool      `gorm:"not null;default:true" json:"rest_day_editing_allowed"`       // 休息日编辑开关
	RestDayAttendanceEnabled    bool      `gorm:"not null;default:true" json:"rest_day_attendance_enabled"`    // 休息日是否参与考勤
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

func (ScheduleSetting) TableName() string {
	return "schedule_settings"
}
