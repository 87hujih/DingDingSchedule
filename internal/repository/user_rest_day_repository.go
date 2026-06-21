package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UserRestDayRepository 用户休息日数据访问接口
type UserRestDayRepository interface {
	Upsert(ctx context.Context, record *model.UserRestDay) error
	Unset(ctx context.Context, userID uint) error
	FindByUserID(ctx context.Context, userID uint) (*model.UserRestDay, error)
	ListByDayOfWeek(ctx context.Context, dayOfWeek int) ([]model.UserRestDay, error)
	ListByUserIDs(ctx context.Context, userIDs []uint) ([]model.UserRestDay, error)
	ListAll(ctx context.Context) ([]model.UserRestDay, error)
}

type userRestDayRepository struct {
	db *gorm.DB
}

func NewUserRestDayRepository(db *gorm.DB) UserRestDayRepository {
	return &userRestDayRepository{db: db}
}

func (r *userRestDayRepository) Upsert(ctx context.Context, record *model.UserRestDay) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"day_of_week", "updated_at"}),
		}).
		Create(record).Error
}

func (r *userRestDayRepository) Unset(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).
		Model(&model.UserRestDay{}).
		Where("user_id = ?", userID).
		Update("day_of_week", nil).Error
}

func (r *userRestDayRepository) FindByUserID(ctx context.Context, userID uint) (*model.UserRestDay, error) {
	var record model.UserRestDay
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *userRestDayRepository) ListByDayOfWeek(ctx context.Context, dayOfWeek int) ([]model.UserRestDay, error) {
	var records []model.UserRestDay
	err := r.db.WithContext(ctx).Where("day_of_week = ?", dayOfWeek).Find(&records).Error
	return records, err
}

func (r *userRestDayRepository) ListByUserIDs(ctx context.Context, userIDs []uint) ([]model.UserRestDay, error) {
	if len(userIDs) == 0 {
		return []model.UserRestDay{}, nil
	}
	var records []model.UserRestDay
	err := r.db.WithContext(ctx).Where("user_id IN ? AND day_of_week IS NOT NULL", userIDs).Find(&records).Error
	return records, err
}

func (r *userRestDayRepository) ListAll(ctx context.Context) ([]model.UserRestDay, error) {
	var records []model.UserRestDay
	err := r.db.WithContext(ctx).Where("day_of_week IS NOT NULL").Find(&records).Error
	return records, err
}
