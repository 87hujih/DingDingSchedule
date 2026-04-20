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

	// SetAttendanceEnabled 设置考勤开关状态
	SetAttendanceEnabled(ctx context.Context, enabled bool) error

	// IsAttendanceEnabled 检查考勤是否启用
	IsAttendanceEnabled(ctx context.Context) (bool, error)

	// SetScheduleChangeNotifyEnabled 设置课表变更通知开关
	SetScheduleChangeNotifyEnabled(ctx context.Context, enabled bool) error

	// IsScheduleChangeNotifyEnabled 检查课表变更通知是否启用
	IsScheduleChangeNotifyEnabled(ctx context.Context) (bool, error)

	// SetLateNotifyEnabled 设置迟到提醒通知开关
	SetLateNotifyEnabled(ctx context.Context, enabled bool) error

	// IsLateNotifyEnabled 检查迟到提醒通知是否启用
	IsLateNotifyEnabled(ctx context.Context) (bool, error)

	// SetRestDayEditingAllowed 设置休息日编辑权限开关
	SetRestDayEditingAllowed(ctx context.Context, allowed bool) error

	// IsRestDayEditingAllowed 检查休息日编辑权限是否启用
	IsRestDayEditingAllowed(ctx context.Context) (bool, error)

	// SetRestDayAttendanceEnabled 设置休息日是否参与考勤
	SetRestDayAttendanceEnabled(ctx context.Context, enabled bool) error

	// IsRestDayAttendanceEnabled 检查休息日是否参与考勤
	IsRestDayAttendanceEnabled(ctx context.Context) (bool, error)
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

func (r *scheduleSettingRepository) SetAttendanceEnabled(ctx context.Context, enabled bool) error {
	return r.db.WithContext(ctx).
		Model(&model.ScheduleSetting{}).
		Update("attendance_enabled", enabled).Error
}

func (r *scheduleSettingRepository) IsAttendanceEnabled(ctx context.Context) (bool, error) {
	setting, err := r.GetByTenantID(ctx)
	if err != nil {
		return true, err // 默认启用
	}
	return setting.AttendanceEnabled, nil
}

func (r *scheduleSettingRepository) SetScheduleChangeNotifyEnabled(ctx context.Context, enabled bool) error {
	return r.db.WithContext(ctx).
		Model(&model.ScheduleSetting{}).
		Update("schedule_change_notify_enabled", enabled).Error
}

func (r *scheduleSettingRepository) IsScheduleChangeNotifyEnabled(ctx context.Context) (bool, error) {
	setting, err := r.GetByTenantID(ctx)
	if err != nil {
		return true, err // 默认启用
	}
	return setting.ScheduleChangeNotifyEnabled, nil
}

func (r *scheduleSettingRepository) SetLateNotifyEnabled(ctx context.Context, enabled bool) error {
	return r.db.WithContext(ctx).
		Model(&model.ScheduleSetting{}).
		Update("late_notify_enabled", enabled).Error
}

func (r *scheduleSettingRepository) IsLateNotifyEnabled(ctx context.Context) (bool, error) {
	setting, err := r.GetByTenantID(ctx)
	if err != nil {
		return true, err // 默认启用
	}
	return setting.LateNotifyEnabled, nil
}

func (r *scheduleSettingRepository) SetRestDayEditingAllowed(ctx context.Context, allowed bool) error {
	return r.db.WithContext(ctx).
		Model(&model.ScheduleSetting{}).
		Update("rest_day_editing_allowed", allowed).Error
}

func (r *scheduleSettingRepository) IsRestDayEditingAllowed(ctx context.Context) (bool, error) {
	setting, err := r.GetByTenantID(ctx)
	if err != nil {
		return true, err // 默认允许
	}
	return setting.RestDayEditingAllowed, nil
}

func (r *scheduleSettingRepository) SetRestDayAttendanceEnabled(ctx context.Context, enabled bool) error {
	return r.db.WithContext(ctx).
		Model(&model.ScheduleSetting{}).
		Update("rest_day_attendance_enabled", enabled).Error
}

func (r *scheduleSettingRepository) IsRestDayAttendanceEnabled(ctx context.Context) (bool, error) {
	setting, err := r.GetByTenantID(ctx)
	if err != nil {
		return true, err // 默认启用
	}
	return setting.RestDayAttendanceEnabled, nil
}
