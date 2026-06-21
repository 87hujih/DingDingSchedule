package repository

import (
	"context"
	"errors"
	"time"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AttendanceManualOverrideRepository 人工覆盖考勤记录仓库
type AttendanceManualOverrideRepository interface {
	UpsertForceOnTime(ctx context.Context, override *model.AttendanceManualOverride) error
	ListByDateSection(ctx context.Context, date time.Time, section int) ([]model.AttendanceManualOverride, error)
}

type attendanceManualOverrideRepository struct {
	db *gorm.DB
}

func NewAttendanceManualOverrideRepository(db *gorm.DB) AttendanceManualOverrideRepository {
	return &attendanceManualOverrideRepository{db: db}
}

func (r *attendanceManualOverrideRepository) UpsertForceOnTime(ctx context.Context, override *model.AttendanceManualOverride) error {
	if override == nil {
		return errors.New("repository: manual override 为空")
	}

	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "date"},
				{Name: "section"},
				{Name: "user_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"week",
				"override_type",
				"operator_id",
				"applied_at",
				"updated_at",
			}),
		}).
		Create(override).Error
}

func (r *attendanceManualOverrideRepository) ListByDateSection(ctx context.Context, date time.Time, section int) ([]model.AttendanceManualOverride, error) {
	var overrides []model.AttendanceManualOverride
	err := r.db.WithContext(ctx).
		Model(&model.AttendanceManualOverride{}).
		Where("date = ? AND section = ?", date, section).
		Order("user_id ASC").
		Find(&overrides).Error
	if err != nil {
		return nil, err
	}
	return overrides, nil
}
