package model

import (
	"time"

	"gorm.io/gorm"
)

// AttendanceManualOverride 人工覆盖考勤记录
type AttendanceManualOverride struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	TenantID     uint           `gorm:"not null;uniqueIndex:uniq_tenant_date_section_user" json:"tenant_id"`
	Date         time.Time      `gorm:"not null;type:date;uniqueIndex:uniq_tenant_date_section_user" json:"date"`
	Week         int            `gorm:"not null" json:"week"`
	Section      int            `gorm:"not null;uniqueIndex:uniq_tenant_date_section_user" json:"section"`
	UserID       uint           `gorm:"not null;uniqueIndex:uniq_tenant_date_section_user" json:"user_id"`
	OverrideType string         `gorm:"size:32;not null" json:"override_type"`
	OperatorID   uint           `gorm:"not null" json:"operator_id"`
	AppliedAt    time.Time      `gorm:"not null" json:"applied_at"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (*AttendanceManualOverride) TableName() string {
	return "attendance_manual_overrides"
}
