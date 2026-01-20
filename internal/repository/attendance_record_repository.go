package repository

import (
	"context"
	"time"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AttendanceRecordRepository 考勤记录仓库接口
type AttendanceRecordRepository interface {
	// Upsert 创建或更新考勤记录（基于 tenant_id + date + section 唯一约束）
	Upsert(ctx context.Context, record *model.AttendanceRecord) error

	// FindByDateSection 根据日期和节次查询考勤记录
	FindByDateSection(ctx context.Context, date time.Time, section int) (*model.AttendanceRecord, error)

	// ListByDate 查询指定日期的所有考勤记录
	ListByDate(ctx context.Context, date time.Time) ([]model.AttendanceRecord, error)

	// ListByDateRange 查询日期范围内的考勤记录
	ListByDateRange(ctx context.Context, startDate, endDate time.Time) ([]model.AttendanceRecord, error)
}

type attendanceRecordRepository struct {
	db *gorm.DB
}

// NewAttendanceRecordRepository 创建考勤记录仓库实例
func NewAttendanceRecordRepository(db *gorm.DB) AttendanceRecordRepository {
	return &attendanceRecordRepository{db: db}
}

// Upsert 创建或更新考勤记录
func (r *attendanceRecordRepository) Upsert(ctx context.Context, record *model.AttendanceRecord) error {
	if record == nil {
		return nil
	}

	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "date"},
				{Name: "section"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"week",
				"on_time_ids",
				"late_ids",
				"leave_ids",
				"absent_ids",
				"updated_at",
			}),
		}).
		Create(record).Error
}

// FindByDateSection 根据日期和节次查询考勤记录
func (r *attendanceRecordRepository) FindByDateSection(ctx context.Context, date time.Time, section int) (*model.AttendanceRecord, error) {
	var record model.AttendanceRecord
	err := r.db.WithContext(ctx).
		Where("date = ? AND section = ?", date, section).
		First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// ListByDate 查询指定日期的所有考勤记录
func (r *attendanceRecordRepository) ListByDate(ctx context.Context, date time.Time) ([]model.AttendanceRecord, error) {
	var records []model.AttendanceRecord
	err := r.db.WithContext(ctx).
		Where("date = ?", date).
		Order("section ASC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	return records, nil
}

// ListByDateRange 查询日期范围内的考勤记录
func (r *attendanceRecordRepository) ListByDateRange(ctx context.Context, startDate, endDate time.Time) ([]model.AttendanceRecord, error) {
	var records []model.AttendanceRecord
	err := r.db.WithContext(ctx).
		Where("date >= ? AND date <= ?", startDate, endDate).
		Order("date ASC, section ASC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	return records, nil
}
