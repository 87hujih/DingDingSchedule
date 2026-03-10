package model

import (
	"time"

	"gorm.io/gorm"
)

// UserRestDay 用户每周休息日设置
type UserRestDay struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	TenantID  uint           `gorm:"not null;uniqueIndex:uniq_tenant_user" json:"tenant_id"`
	UserID    uint           `gorm:"not null;uniqueIndex:uniq_tenant_user" json:"user_id"`
	DayOfWeek int            `gorm:"not null" json:"day_of_week"` // 1=周一, 7=周日
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (*UserRestDay) TableName() string {
	return "user_rest_days"
}
