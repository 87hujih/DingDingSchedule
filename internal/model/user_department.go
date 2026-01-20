package model

// UserDepartment 用户所属部门
type UserDepartment struct {
	TenantID uint  `gorm:"primaryKey" json:"tenant_id"`
	UserID   uint  `gorm:"primaryKey" json:"user_id"`
	DeptID   int64 `gorm:"primaryKey" json:"dept_id"`
}

func (UserDepartment) TableName() string {
	return "user_departments"
}
