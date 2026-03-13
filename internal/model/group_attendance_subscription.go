package model

import (
	"time"

	"gorm.io/gorm"
)

// GroupAttendanceSubscription 群考勤推送订阅
type GroupAttendanceSubscription struct {
	ID             uint           `gorm:"primaryKey"`
	TenantID       uint           `gorm:"not null;uniqueIndex:uniq_tenant_conv"`
	ConversationID string         `gorm:"not null;uniqueIndex:uniq_tenant_conv"`
	GroupName      string
	EnabledByUID   uint
	CreatedAt      time.Time
	DeletedAt      gorm.DeletedAt `gorm:"index"`
}

func (*GroupAttendanceSubscription) TableName() string {
	return "group_attendance_subscriptions"
}
