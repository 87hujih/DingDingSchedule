package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

// CourseRepository 课表仓库接口
type CourseRepository interface {
	DeleteByUserSemester(ctx context.Context, userID uint, semester string) error
	BatchCreate(ctx context.Context, courses []model.Course) error
	ListByUserSemester(ctx context.Context, userID uint, semester string) ([]model.Course, error)
	ReplaceByUserSemester(ctx context.Context, userID uint, semester string, courses []model.Course) error

	// 单条 CRUD
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

// DeleteByUserSemester 根据用户 ID 删除课表
func (r *courseRepository) DeleteByUserSemester(ctx context.Context, userID uint, semester string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND semester = ?", userID, semester).
		Delete(&model.Course{}).Error
}

// BatchCreate 批量创建课表
func (r *courseRepository) BatchCreate(ctx context.Context, courses []model.Course) error {
	if len(courses) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Create(&courses).Error
}

// ListByUserSemester 用户课程列表
func (r *courseRepository) ListByUserSemester(ctx context.Context, userID uint, semester string) ([]model.Course, error) {
	var courses []model.Course
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND semester = ?", userID, semester).
		Order("day_of_week ASC, section ASC, id ASC").
		Find(&courses).Error; err != nil {
		return nil, err
	}
	return courses, nil
}

// ReplaceByUserSemester 在事务中先删除指定用户+学期的课程，再插入新课程
func (r *courseRepository) ReplaceByUserSemester(ctx context.Context, userID uint, semester string, courses []model.Course) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Where("user_id = ? AND semester = ?", userID, semester).
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
	return r.db.WithContext(ctx).Create(course).Error
}

// Update 更新课程（仅更新非零字段）
func (r *courseRepository) Update(ctx context.Context, course *model.Course) error {
	return r.db.WithContext(ctx).Model(course).Updates(course).Error
}

// Delete 删除课程（软删除）
func (r *courseRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Course{}, id).Error
}
