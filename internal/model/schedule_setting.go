package model

import "time"

// ScheduleSetting 作息配置设置
type ScheduleSetting struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	TenantID    uint      `gorm:"not null;uniqueIndex:uniq_tenant" json:"tenant_id"`
	CurrentMode string    `gorm:"size:20;not null;default:school" json:"current_mode"` // school/holiday
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (ScheduleSetting) TableName() string {
	return "schedule_settings"
}
