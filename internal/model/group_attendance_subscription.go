package model

import (
	"time"

	"gorm.io/gorm"
)

// GroupAttendanceSubscription 群考勤推送订阅
type GroupAttendanceSubscription struct {
	ID             uint   `gorm:"primaryKey"`
	TenantID       uint   `gorm:"not null;uniqueIndex:uniq_tenant_conv"`
	ConversationID string `gorm:"not null;size:191;uniqueIndex:uniq_tenant_conv"`
	GroupName      string
	EnabledByUID   uint
	DeptIDsJSON    string `gorm:"column:dept_ids_json"`  // JSON 数组，为空表示全部部门
	PushEnabled    bool   `gorm:"not null;default:true"` // 后台控制是否自动推送
	CreatedAt      time.Time
	DeletedAt      gorm.DeletedAt `gorm:"index"`
}

func (*GroupAttendanceSubscription) TableName() string {
	return "group_attendance_subscriptions"
}
