package model

import "time"

// Semester 学期配置
type Semester struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	TenantID   uint      `gorm:"not null;index" json:"tenant_id"`
	Name       string    `gorm:"size:50;not null" json:"name"`                  // 学期名称
	StartDate  time.Time `gorm:"type:date;not null" json:"start_date"`          // 学期开始日期（第1周周一）
	TotalWeeks int       `gorm:"not null;default:20" json:"total_weeks"`        // 总周数
	IsActive   bool      `gorm:"not null;default:false;index" json:"is_active"` // 是否当前生效学期
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (Semester) TableName() string { return "semesters" }
