package model

import "time"

// User 用户模型
type User struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	DingUserID string    `gorm:"uniqueIndex;size:64" json:"ding_user_id"` // 钉钉用户ID
	Name       string    `gorm:"size:32" json:"name"`
	Phone      string    `gorm:"size:20" json:"phone"`
	Avatar     string    `json:"avatar"`
	DeptID     uint      `gorm:"index" json:"dept_id"`
	Status     int       `gorm:"default:1" json:"status"` // 1-正常 0-禁用
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// 关联
	Dept *Dept `gorm:"foreignKey:DeptID" json:"dept,omitempty"`
}

func (User) TableName() string {
	return "users"
}
