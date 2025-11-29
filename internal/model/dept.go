package model

import "time"

// Dept 部门模型
type Dept struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	DingDeptID string    `gorm:"uniqueIndex;size:64" json:"ding_dept_id"` // 钉钉部门ID
	Name       string    `gorm:"size:64" json:"name"`
	ParentID   uint      `gorm:"index" json:"parent_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (Dept) TableName() string {
	return "depts"
}
