package service

import (
	"context"

	"schedule_server/internal/model"
	"schedule_server/internal/repository"

	"go.uber.org/zap"
)

// AuditLogService 审计日志服务
type AuditLogService struct {
	auditRepo repository.AuditLogRepository
	logger    *zap.SugaredLogger
}

// NewAuditLogService 创建审计日志服务实例
func NewAuditLogService(auditRepo repository.AuditLogRepository, logger *zap.SugaredLogger) *AuditLogService {
	return &AuditLogService{
		auditRepo: auditRepo,
		logger:    logger,
	}
}

// Create 写入一条审计日志
func (s *AuditLogService) Create(ctx context.Context, log *model.AuditLog) error {
	if err := s.auditRepo.Create(ctx, log); err != nil {
		s.logger.Errorw("审计日志写入失败", "error", err)
		return err
	}
	return nil
}

// List 分页查询审计日志
func (s *AuditLogService) List(ctx context.Context, filter repository.AuditLogFilter) ([]*model.AuditLog, int64, error) {
	return s.auditRepo.List(ctx, filter)
}
