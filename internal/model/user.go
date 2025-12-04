package model

import (
	"schedule_server/internal/consts"
	"time"

	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	DingUserID string         `gorm:"uniqueIndex;size:64" json:"ding_user_id"` // 钉钉用户ID
	Name       string         `gorm:"size:32" json:"name"`
	Phone      string         `gorm:"size:20" json:"phone"`
	Avatar     string         `json:"avatar"`
	Role       int            `gorm:"default:0" json:"role"`   // 用户角色(0:普通成员;1:小组长;2:实验室管理员;3:超级管理员)
	Status     int            `gorm:"default:1" json:"status"` // 用户是否参与考勤(1:参与;0:不参与)
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"` // 软删除
}

// RoleName 获取角色名称
func (u *User) RoleName() string {
	return consts.RoleName(u.Role)
}

func (*User) TableName() string {
	return "users"
}
