package model

import (
	"time"

	"gorm.io/gorm"
)

// AttendanceRecord 考勤记录模型
type AttendanceRecord struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	TenantID      uint           `gorm:"not null;uniqueIndex:uniq_tenant_date_section" json:"tenant_id"`
	Date          time.Time      `gorm:"not null;uniqueIndex:uniq_tenant_date_section;type:date" json:"date"`
	Week          int            `gorm:"not null" json:"week"`
	Section       int            `gorm:"not null;uniqueIndex:uniq_tenant_date_section" json:"section"`
	OnTimeIDs     string         `gorm:"type:text" json:"on_time_ids"`     // JSON数组：正常打卡人员ID
	LeaveIDs      string         `gorm:"type:text" json:"leave_ids"`       // JSON数组：请假人员ID
	NotArrivedIDs string         `gorm:"type:text" json:"not_arrived_ids"` // JSON数组：未到人员ID（含迟到和缺勤）
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"` // 软删除
}

func (*AttendanceRecord) TableName() string {
	return "attendance_records"
}
