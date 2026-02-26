package model

import "time"

// AuditLog 操作审计日志（不可删除的合规记录）
type AuditLog struct {
	ID          uint   `gorm:"primaryKey"`
	TenantID    uint   `gorm:"not null;index"`
	UserID      uint   `gorm:"not null;index"`
	UserName    string `gorm:"size:32"`
	UserRole    int
	Method      string `gorm:"size:10"`
	Path        string `gorm:"size:255;index"`
	StatusCode  int
	Duration    int64     // 毫秒
	IPAddress   string    `gorm:"size:64"`
	RequestBody string    `gorm:"type:text"`
	CreatedAt   time.Time `gorm:"index"`
}

func (AuditLog) TableName() string { return "audit_logs" }
