package model

import (
	"time"

	"gorm.io/gorm"
)

type Course struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	UserID     uint           `gorm:"index;not null;comment:用户ID"`
	Semester   string         `gorm:"type:varchar(20);comment:学期(如2025-Spring)"`
	CourseName string         `gorm:"type:varchar(100);not null"`
	Teacher    string         `gorm:"type:varchar(50)"`
	Location   string         `gorm:"type:varchar(100)"`
	DayOfWeek  int            `gorm:"comment:星期几(1-7)"`
	Section    int            `gorm:"comment:大节次(1=1-2节, 2=3-4节...)"`
	WeekList   string         `gorm:"type:varchar(255);comment:周次列表(逗号分隔)"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"` // 软删除
}

func (*Course) TableName() string {
	return "courses"
}
