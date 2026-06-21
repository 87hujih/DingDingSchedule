package repository

import (
	"context"
	"errors"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

// CourseRepository 课表仓库接口
type CourseRepository interface {
	DeleteByUser(ctx context.Context, userID uint) error
	BatchCreate(ctx context.Context, courses []model.Course) error
	ListByUser(ctx context.Context, userID uint) ([]model.Course, error)
	ListByUserPaged(ctx context.Context, userID uint, page, pageSize int) ([]model.Course, int64, error)
	// ListByUsersDaySection 按用户集合+星期+大节查询课程（用于判定忙闲）
	ListByUsersDaySection(ctx context.Context, userIDs []uint, dayOfWeek, section int) ([]model.Course, error)
	// ListCoursesByUsersDays 按用户集合+星期范围查询课程（整周批量，不过滤 section）
	ListCoursesByUsersDays(ctx context.Context, userIDs []uint, dayOfWeeks []int) ([]model.Course, error)
	ReplaceByUser(ctx context.Context, userID uint, courses []model.Course) error
	GetByID(ctx context.Context, id uint) (*model.Course, error)
	Create(ctx context.Context, course *model.Course) error
	Update(ctx context.Context, course *model.Course) error
	Delete(ctx context.Context, id uint) error
}

type courseRepository struct {
	db *gorm.DB
}

// NewCourseRepository 创建课表仓库实例
func NewCourseRepository(db *gorm.DB) CourseRepository {
	return &courseRepository{db: db}
}

// DeleteByUser 根据用户 ID 删除课表
func (r *courseRepository) DeleteByUser(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&model.Course{}).Error
}

// BatchCreate 批量创建课表
func (r *courseRepository) BatchCreate(ctx context.Context, courses []model.Course) error {
	if len(courses) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&courses).Error
}

// ListByUser 用户课程列表
func (r *courseRepository) ListByUser(ctx context.Context, userID uint) ([]model.Course, error) {
	var courses []model.Course
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("day_of_week ASC, section ASC, id ASC").
		Find(&courses).Error; err != nil {
		return nil, err
	}
	return courses, nil
}

// ListByUserPaged 用户课程列表（分页）
func (r *courseRepository) ListByUserPaged(ctx context.Context, userID uint, page, pageSize int) ([]model.Course, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	query := r.db.WithContext(ctx).Model(&model.Course{}).
		Where("user_id = ?", userID)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if total == 0 || int64(offset) >= total {
		return []model.Course{}, total, nil
	}

	var courses []model.Course
	if err := query.Order("day_of_week ASC, section ASC, id ASC").
		Limit(pageSize).
		Offset(offset).
		Find(&courses).Error; err != nil {
		return nil, 0, err
	}

	return courses, total, nil
}

// ListByUsersDaySection 查询一批用户在指定星期几、节次的课程
func (r *courseRepository) ListByUsersDaySection(
	ctx context.Context,
	userIDs []uint,
	dayOfWeek int,
	section int,
) ([]model.Course, error) {
	if len(userIDs) == 0 {
		return []model.Course{}, nil
	}
	if dayOfWeek <= 0 || section <= 0 {
		return []model.Course{}, nil
	}

	var courses []model.Course
	if err := r.db.WithContext(ctx).
		Where("user_id IN ? AND day_of_week = ? AND section = ?", userIDs, dayOfWeek, section).
		Find(&courses).Error; err != nil {
		return nil, err
	}
	return courses, nil
}

// ListCoursesByUsersDays 查询一批用户在指定星期范围内的所有课程（整周批量，不过滤 section）
func (r *courseRepository) ListCoursesByUsersDays(
	ctx context.Context,
	userIDs []uint,
	dayOfWeeks []int,
) ([]model.Course, error) {
	if len(userIDs) == 0 || len(dayOfWeeks) == 0 {
		return []model.Course{}, nil
	}
	var courses []model.Course
	if err := r.db.WithContext(ctx).
		Where("user_id IN ? AND day_of_week IN ?", userIDs, dayOfWeeks).
		Find(&courses).Error; err != nil {
		return nil, err
	}
	return courses, nil
}

// ReplaceByUser 在事务中先删除指定用户的课程，再插入新课程
func (r *courseRepository) ReplaceByUser(ctx context.Context, userID uint, courses []model.Course) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().
			Where("user_id = ?", userID).
			Delete(&model.Course{}).Error; err != nil {
			return err
		}

		if len(courses) == 0 {
			return nil
		}
		return tx.Create(&courses).Error
	})
}

// GetByID 根据 ID 查询单条课程
func (r *courseRepository) GetByID(ctx context.Context, id uint) (*model.Course, error) {
	var course model.Course
	if err := r.db.WithContext(ctx).First(&course, id).Error; err != nil {
		return nil, err
	}
	return &course, nil
}

// Create 创建单条课程
func (r *courseRepository) Create(ctx context.Context, course *model.Course) error {
	if course == nil {
		return errors.New("repository: course 为空")
	}
	return r.db.WithContext(ctx).Create(course).Error
}

// Update 更新课程（仅更新非零字段）
func (r *courseRepository) Update(ctx context.Context, course *model.Course) error {
	if course == nil {
		return errors.New("repository: course 为空")
	}
	return r.db.WithContext(ctx).
		Model(&model.Course{}).
		Where("id = ?", course.ID).
		Updates(course).Error
}

// Delete 删除课程（软删除）
func (r *courseRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Course{}).Error
}
