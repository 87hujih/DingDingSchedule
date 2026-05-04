package model

import "time"

// ScheduleMode 作息模式常量（顶层模式）
const (
	ScheduleModeSchool  = "school"  // 上学模式
	ScheduleModeHoliday = "holiday" // 假期模式
)

// SchoolSeason 上学模式内的季节常量
const (
	SchoolSeasonSummer = "summer" // 夏季作息
	SchoolSeasonWinter = "winter" // 冬季作息
)

// SchedulePeriodMode 作息时间段的模式标识（用于 schedule_periods.mode 字段）
const (
	SchedulePeriodModeSchoolSummer = "school_summer" // 夏季上学作息
	SchedulePeriodModeSchoolWinter = "school_winter" // 冬季上学作息
	SchedulePeriodModeHoliday      = "holiday"       // 假期作息
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
