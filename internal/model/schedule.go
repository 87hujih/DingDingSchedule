package model

import "time"

// Schedule 排班模型
type Schedule struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index" json:"user_id"`
	Date      time.Time `gorm:"index" json:"date"`       // 排班日期
	StartTime time.Time `json:"start_time"`              // 上班时间
	EndTime   time.Time `json:"end_time"`                // 下班时间
	Status    int       `gorm:"default:0" json:"status"` // 0-待签到 1-已签到 2-已签退 3-缺勤
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 关联
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (Schedule) TableName() string {
	return "schedules"
}
