package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

type SchedulePeriodRepository interface {
	// ListActiveByMode 获取指定模式下的作息时间配置
	ListActiveByMode(ctx context.Context, mode string) ([]*model.SchedulePeriod, error)

	// ListActive 获取当前模式下的作息时间配置（根据 schedule_settings）
	ListActive(ctx context.Context) ([]*model.SchedulePeriod, error)

	// ListAllByMode 获取指定模式下的所有配置（包括禁用的）
	ListAllByMode(ctx context.Context, mode string) ([]*model.SchedulePeriod, error)

	// Create 创建作息时间配置
	Create(ctx context.Context, period *model.SchedulePeriod) error

	// Update 更新作息时间配置
	Update(ctx context.Context, period *model.SchedulePeriod) error

	// Delete 删除作息时间配置
	Delete(ctx context.Context, id uint) error
}

type schedulePeriodRepository struct {
	db          *gorm.DB
	settingRepo ScheduleSettingRepository
}

func NewSchedulePeriodRepository(db *gorm.DB, settingRepo ScheduleSettingRepository) SchedulePeriodRepository {
	return &schedulePeriodRepository{db: db, settingRepo: settingRepo}
}

func (r *schedulePeriodRepository) ListActiveByMode(ctx context.Context, mode string) ([]*model.SchedulePeriod, error) {
	var periods []*model.SchedulePeriod
	err := r.db.WithContext(ctx).
		Where("mode = ? AND is_active = ?", mode, true).
		Order("sort_order ASC").
		Find(&periods).Error
	return periods, err
}

func (r *schedulePeriodRepository) ListActive(ctx context.Context) ([]*model.SchedulePeriod, error) {
	// 获取当前模式
	setting, err := r.settingRepo.GetByTenantID(ctx)
	if err != nil {
		// 默认使用上学模式
		return r.ListActiveByMode(ctx, model.ScheduleModeSchool)
	}
	return r.ListActiveByMode(ctx, setting.CurrentMode)
}

func (r *schedulePeriodRepository) ListAllByMode(ctx context.Context, mode string) ([]*model.SchedulePeriod, error) {
	var periods []*model.SchedulePeriod
	err := r.db.WithContext(ctx).
		Where("mode = ?", mode).
		Order("sort_order ASC").
		Find(&periods).Error
	return periods, err
}

func (r *schedulePeriodRepository) Create(ctx context.Context, period *model.SchedulePeriod) error {
	return r.db.WithContext(ctx).Create(period).Error
}

func (r *schedulePeriodRepository) Update(ctx context.Context, period *model.SchedulePeriod) error {
	return r.db.WithContext(ctx).Save(period).Error
}

func (r *schedulePeriodRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.SchedulePeriod{}, id).Error
}
