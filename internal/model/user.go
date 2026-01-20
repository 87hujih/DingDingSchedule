package model

import (
	"time"

	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	TenantID       uint           `gorm:"not null;uniqueIndex:uniq_tenant_ding_user;index" json:"tenant_id"`
	DingUserID     string         `gorm:"size:64;not null;uniqueIndex:uniq_tenant_ding_user" json:"ding_user_id"` // 钉钉用户ID（企业内唯一）
	Name           string         `gorm:"size:32" json:"name"`
	NamePinyin     string         `gorm:"size:128;index" json:"-"` // 姓名全拼（用于搜索）
	NamePinyinAbbr string         `gorm:"size:32;index" json:"-"`  // 姓名拼音首字母（用于搜索）
	Phone          string         `gorm:"size:20" json:"phone"`
	Avatar         string         `json:"avatar"`
	Role           int            `gorm:"default:0" json:"role"`   // 用户角色(0:普通用户;1:管理员;2:超级管理员)
	Status         int            `gorm:"default:1" json:"status"` // 用户是否参与考勤(1:参与;0:不参与)
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"` // 软删除
}

func (*User) TableName() string {
	return "users"
}
