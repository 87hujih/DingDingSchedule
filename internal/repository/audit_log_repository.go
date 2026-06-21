package repository

import (
	"context"
	"time"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

// AuditLogFilter 审计日志查询过滤条件
type AuditLogFilter struct {
	UserID    uint
	Method    string
	Path      string
	StartDate time.Time
	EndDate   time.Time
	Page      int
	PageSize  int
}

// AuditLogRepository 审计日志仓库接口
type AuditLogRepository interface {
	Create(ctx context.Context, log *model.AuditLog) error
	List(ctx context.Context, filter AuditLogFilter) ([]*model.AuditLog, int64, error)
}

type auditLogRepository struct {
	db *gorm.DB
}

// NewAuditLogRepository 创建审计日志仓库实例
func NewAuditLogRepository(db *gorm.DB) AuditLogRepository {
	return &auditLogRepository{db: db}
}

func (r *auditLogRepository) Create(ctx context.Context, log *model.AuditLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *auditLogRepository) List(ctx context.Context, filter AuditLogFilter) ([]*model.AuditLog, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.AuditLog{})

	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.Method != "" {
		query = query.Where("method = ?", filter.Method)
	}
	if filter.Path != "" {
		query = query.Where("path LIKE ?", "%"+filter.Path+"%")
	}
	if !filter.StartDate.IsZero() {
		query = query.Where("created_at >= ?", filter.StartDate)
	}
	if !filter.EndDate.IsZero() {
		query = query.Where("created_at < ?", filter.EndDate)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	var logs []*model.AuditLog
	err := query.Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	return logs, total, nil
}
