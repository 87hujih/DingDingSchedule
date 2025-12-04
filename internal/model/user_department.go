package model

// UserDepartment 用户所属部门
type UserDepartment struct {
	UserID uint `gorm:"primaryKey" json:"user_id"`
	DeptID uint `gorm:"primaryKey" json:"dept_id"`
}

func (UserDepartment) TableName() string {
	return "user_departments"
}
