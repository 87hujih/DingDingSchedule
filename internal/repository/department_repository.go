package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DepartmentRepository 部门仓库接口
type DepartmentRepository interface {
	// SyncAll 同步所有部门（事务：先删除不在列表中的，再upsert）
	SyncAll(ctx context.Context, depts []model.Department, deptIDs []int64) error
	// FindLeaf 查询所有叶子部门
	FindLeaf(ctx context.Context) ([]model.Department, error)
	// FindByID 根据ID查询部门
	FindByID(ctx context.Context, deptID int64) (*model.Department, error)
	// UpdateStatus 更新部门考勤状态
	UpdateStatus(ctx context.Context, deptID int64, status int) error
	// Delete 软删除部门
	Delete(ctx context.Context, deptID int64) error
}

type departmentRepository struct {
	db *gorm.DB
}

// NewDepartmentRepository 创建部门仓库实例
func NewDepartmentRepository(db *gorm.DB) DepartmentRepository {
	return &departmentRepository{db: db}
}

// SyncAll 同步所有部门（事务：先删除不在列表中的，再upsert）
func (r *departmentRepository) SyncAll(ctx context.Context, depts []model.Department, deptIDs []int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 软删除钉钉中已不存在的部门
		if len(deptIDs) > 0 {
			if err := tx.Where("dept_id NOT IN ?", deptIDs).Delete(&model.Department{}).Error; err != nil {
				return err
			}
		}

		// 2. 批量 upsert（存在则更新，不存在则插入）
		if len(depts) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "tenant_id"},
					{Name: "dept_id"},
				},
				DoUpdates: clause.AssignmentColumns([]string{"name", "parent_id", "is_leaf", "status", "deleted_at"}),
			}).Create(&depts).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// FindLeaf 查询所有叶子部门
func (r *departmentRepository) FindLeaf(ctx context.Context) ([]model.Department, error) {
	var depts []model.Department
	if err := r.db.WithContext(ctx).Where("is_leaf = ?", true).Find(&depts).Error; err != nil {
		return nil, err
	}
	return depts, nil
}

// FindByID 根据ID查询部门
func (r *departmentRepository) FindByID(ctx context.Context, deptID int64) (*model.Department, error) {
	var dept model.Department
	if err := r.db.WithContext(ctx).Where("dept_id = ?", deptID).First(&dept).Error; err != nil {
		return nil, err
	}
	return &dept, nil
}

// UpdateStatus 更新部门考勤状态
func (r *departmentRepository) UpdateStatus(ctx context.Context, deptID int64, status int) error {
	// 先检查记录是否存在，避免值相同时 RowsAffected=0 被误判为不存在
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.Department{}).Where("dept_id = ?", deptID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}

	return r.db.WithContext(ctx).
		Model(&model.Department{}).
		Where("dept_id = ?", deptID).
		Update("status", status).Error
}

// Delete 软删除部门
func (r *departmentRepository) Delete(ctx context.Context, deptID int64) error {
	result := r.db.WithContext(ctx).
		Where("dept_id = ?", deptID).
		Delete(&model.Department{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
