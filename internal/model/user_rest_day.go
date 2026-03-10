package model

import "time"

// UserRestDay 用户每周休息日设置
// DayOfWeek 为 NULL 表示用户已取消休息日（记录保留，避免软删除干扰 Upsert）
type UserRestDay struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TenantID  uint      `gorm:"not null;uniqueIndex:uniq_tenant_user" json:"tenant_id"`
	UserID    uint      `gorm:"not null;uniqueIndex:uniq_tenant_user" json:"user_id"`
	DayOfWeek *int      `json:"day_of_week"` // 1=周一, 7=周日; NULL=未设置
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (*UserRestDay) TableName() string {
	return "user_rest_days"
}
