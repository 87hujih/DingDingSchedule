package dto

import (
	"time"

	"schedule_server/internal/consts"
	"schedule_server/internal/model"
)

// AuditLogItem 审计日志响应条目
type AuditLogItem struct {
	ID          uint      `json:"id"`
	UserID      uint      `json:"user_id"`
	UserName    string    `json:"user_name"`
	UserRole    string    `json:"user_role"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	StatusCode  int       `json:"status_code"`
	Duration    int64     `json:"duration_ms"`
	IPAddress   string    `json:"ip_address"`
	RequestBody string    `json:"request_body"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewAuditLogItem 将 model 转换为 DTO
func NewAuditLogItem(log *model.AuditLog) *AuditLogItem {
	return &AuditLogItem{
		ID:          log.ID,
		UserID:      log.UserID,
		UserName:    log.UserName,
		UserRole:    consts.RoleName(log.UserRole),
		Method:      log.Method,
		Path:        log.Path,
		StatusCode:  log.StatusCode,
		Duration:    log.Duration,
		IPAddress:   log.IPAddress,
		RequestBody: log.RequestBody,
		CreatedAt:   log.CreatedAt,
	}
}

// AuditLogListResponse 审计日志列表响应
type AuditLogListResponse struct {
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Total    int64           `json:"total"`
	Items    []*AuditLogItem `json:"items"`
}
