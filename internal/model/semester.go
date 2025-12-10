package model

import (
	"time"

	"gorm.io/gorm"
)

// Semester 学期配置
type Semester struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"type:varchar(20);uniqueIndex;not null;comment:学期标识(如2025-Spring)" json:"name"`
	StartDate time.Time      `gorm:"not null;comment:学期第一周周一" json:"start_date"`
	TotalWeek int            `gorm:"default:20;comment:学期总周数" json:"total_week"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (*Semester) TableName() string {
	return "semesters"
}
