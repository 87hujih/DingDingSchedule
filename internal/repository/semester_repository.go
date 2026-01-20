package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

// SemesterRepository 学期仓库接口
type SemesterRepository interface {
	GetActiveSemester(ctx context.Context) (*model.Semester, error)
	GetByID(ctx context.Context, id uint) (*model.Semester, error)
	DeactivateAllByTenant(ctx context.Context, tenantID uint) error
}

type semesterRepository struct {
	db *gorm.DB
}

// NewSemesterRepository 创建学期仓库实例
func NewSemesterRepository(db *gorm.DB) SemesterRepository {
	return &semesterRepository{db: db}
}

// GetActiveSemester 获取当前租户的激活学期
func (r *semesterRepository) GetActiveSemester(ctx context.Context) (*model.Semester, error) {
	var semester model.Semester
	if err := r.db.WithContext(ctx).
		Where("is_active = ?", true).
		First(&semester).Error; err != nil {
		return nil, err
	}
	return &semester, nil
}

// GetByID 根据 ID 查询学期
func (r *semesterRepository) GetByID(ctx context.Context, id uint) (*model.Semester, error) {
	var semester model.Semester
	if err := r.db.WithContext(ctx).First(&semester, id).Error; err != nil {
		return nil, err
	}
	return &semester, nil
}

// DeactivateAllByTenant 将指定租户的所有学期设为非激活
func (r *semesterRepository) DeactivateAllByTenant(ctx context.Context, tenantID uint) error {
	return r.db.WithContext(ctx).
		Model(&model.Semester{}).
		Where("tenant_id = ? AND is_active = ?", tenantID, true).
		Update("is_active", false).Error
}
