package model

// UserType 用户身份
type UserType struct {
	ID   int    `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Name string `json:"name"` // 身份名称
}

func (UserType) TableName() string {
	return "user_types"
}
