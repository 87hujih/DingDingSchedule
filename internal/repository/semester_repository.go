package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

// SemesterRepository 学期仓库接口
type SemesterRepository interface {
	GetByName(ctx context.Context, name string) (*model.Semester, error)
	Create(ctx context.Context, semester *model.Semester) error
	Update(ctx context.Context, semester *model.Semester) error

	GetByID(ctx context.Context, id uint) (*model.Semester, error)
	List(ctx context.Context) ([]model.Semester, error)
	Delete(ctx context.Context, id uint) error
}

type semesterRepository struct {
	db *gorm.DB
}

// NewSemesterRepository 创建学期仓库实例
func NewSemesterRepository(db *gorm.DB) SemesterRepository {
	return &semesterRepository{db: db}
}

// GetByName 根据学期名称查询
func (r *semesterRepository) GetByName(ctx context.Context, name string) (*model.Semester, error) {
	var sem model.Semester
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&sem).Error; err != nil {
		return nil, err
	}
	return &sem, nil
}

// Create 创建学期
func (r *semesterRepository) Create(ctx context.Context, semester *model.Semester) error {
	return r.db.WithContext(ctx).Create(semester).Error
}

// Update 更新学期
func (r *semesterRepository) Update(ctx context.Context, semester *model.Semester) error {
	return r.db.WithContext(ctx).Save(semester).Error
}

// GetByID 根据 ID 查询学期
func (r *semesterRepository) GetByID(ctx context.Context, id uint) (*model.Semester, error) {
	var sem model.Semester
	if err := r.db.WithContext(ctx).First(&sem, id).Error; err != nil {
		return nil, err
	}
	return &sem, nil
}

// List 查询所有学期
func (r *semesterRepository) List(ctx context.Context) ([]model.Semester, error) {
	var semesters []model.Semester
	if err := r.db.WithContext(ctx).Order("start_date DESC").Find(&semesters).Error; err != nil {
		return nil, err
	}
	return semesters, nil
}

// Delete 删除学期（软删除）
func (r *semesterRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Semester{}, id).Error
}
