package model

import "time"

// SystemLog 系统错误日志（Error 级别及以上，入库方便在线排查）
type SystemLog struct {
	ID        uint      `gorm:"primaryKey"`
	Level     string    `gorm:"size:10;index"`
	Caller    string    `gorm:"size:255"`
	Message   string    `gorm:"type:text"`
	Fields    string    `gorm:"type:text"` // JSON 序列化的结构化字段
	Stack     string    `gorm:"type:text"` // 仅 Error+ 有堆栈
	CreatedAt time.Time `gorm:"index"`
}

func (SystemLog) TableName() string { return "system_logs" }
