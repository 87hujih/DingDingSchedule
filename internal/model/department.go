package model

import "gorm.io/gorm"

// Department 部门信息
type Department struct {
	TenantID  uint           `gorm:"primaryKey" json:"tenant_id"`
	DeptID    int64          `gorm:"primaryKey" json:"dept_id"` // 钉钉部门ID（企业内唯一）
	Name      string         `gorm:"size:128" json:"name"`
	ParentID  int64          `gorm:"index" json:"parent_id"`  // 父部门ID
	IsLeaf    bool           `gorm:"index" json:"is_leaf"`    // 是否叶子部门（无子部门）
	Status    int            `gorm:"default:1" json:"status"` // 部门是否参与考勤(1:参与;0:不参与)
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Department) TableName() string {
	return "departments"
}
