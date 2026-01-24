package model

import "time"

// ScheduleMode 作息模式常量
const (
	ScheduleModeSchool  = "school"  // 上学模式
	ScheduleModeHoliday = "holiday" // 假期模式
)

// SchedulePeriod 作息时间配置
type SchedulePeriod struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	TenantID  uint       `gorm:"not null;index:idx_tenant_mode" json:"tenant_id"`
	Mode      string     `gorm:"size:20;not null;default:school;index:idx_tenant_mode" json:"mode"` // school/holiday
	Name      string     `gorm:"size:50;not null" json:"name"`
	StartTime string     `gorm:"size:10;not null" json:"start_time"` // 格式: "08:00:00"
	EndTime   string     `gorm:"size:10;not null" json:"end_time"`   // 格式: "09:40:00"
	SortOrder int        `gorm:"not null;default:0" json:"sort_order"`
	IsActive  bool       `gorm:"not null;default:true" json:"is_active"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `gorm:"index" json:"deleted_at,omitempty"`
}

func (SchedulePeriod) TableName() string {
	return "schedule_periods"
}
