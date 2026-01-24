package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ScheduleSettingRepository interface {
	// GetByTenantID 获取租户的作息设置（自动从 context 获取 tenant_id）
	GetByTenantID(ctx context.Context) (*model.ScheduleSetting, error)

	// Upsert 创建或更新作息设置
	Upsert(ctx context.Context, setting *model.ScheduleSetting) error

	// SwitchMode 切换作息模式
	SwitchMode(ctx context.Context, mode string) error
}

type scheduleSettingRepository struct {
	db *gorm.DB
}

func NewScheduleSettingRepository(db *gorm.DB) ScheduleSettingRepository {
	return &scheduleSettingRepository{db: db}
}

func (r *scheduleSettingRepository) GetByTenantID(ctx context.Context) (*model.ScheduleSetting, error) {
	var setting model.ScheduleSetting
	err := r.db.WithContext(ctx).First(&setting).Error
	return &setting, err
}

func (r *scheduleSettingRepository) Upsert(ctx context.Context, setting *model.ScheduleSetting) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"current_mode", "updated_at"}),
		}).
		Create(setting).Error
}

func (r *scheduleSettingRepository) SwitchMode(ctx context.Context, mode string) error {
	return r.db.WithContext(ctx).
		Model(&model.ScheduleSetting{}).
		Update("current_mode", mode).Error
}
