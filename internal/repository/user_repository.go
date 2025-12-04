package repository

import (
	"context"
	"errors"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

// 预定义错误
var (
	ErrUserNotFound = errors.New("repository: 用户不存在")
)

// UserRepository 用户仓库接口
type UserRepository interface {
	// FindByID 根据ID查询用户
	FindByID(ctx context.Context, id uint) (*model.User, error)
	// FindByDingUserID 根据钉钉用户ID查询用户
	FindByDingUserID(ctx context.Context, dingUserID string) (*model.User, error)
	// Create 创建用户
	Create(ctx context.Context, user *model.User) error
	// Update 更新用户
	Update(ctx context.Context, user *model.User) error
	// Upsert 创建或更新用户（根据ding_user_id判断）
	Upsert(ctx context.Context, user *model.User) error
	// SyncDepartments 同步用户所属部门（事务：先删后插）
	SyncDepartments(ctx context.Context, userID uint, deptIDs []int64) error
	// FindDepartmentIDs 查询用户所属部门ID列表
	FindDepartmentIDs(ctx context.Context, userID uint) ([]int64, error)
}

type userRepository struct {
	db *gorm.DB
}

// NewUserRepository 创建用户仓库实例
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

// FindByID 根据ID查询用户
func (r *userRepository) FindByID(ctx context.Context, id uint) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// FindByDingUserID 根据钉钉用户ID查询用户
func (r *userRepository) FindByDingUserID(ctx context.Context, dingUserID string) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Where("ding_user_id = ?", dingUserID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// Create 创建用户
func (r *userRepository) Create(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

// Update 更新用户
func (r *userRepository) Update(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Save(user).Error
}

// Upsert 创建或更新用户（根据ding_user_id判断）
func (r *userRepository) Upsert(ctx context.Context, user *model.User) error {
	existing, err := r.FindByDingUserID(ctx, user.DingUserID)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return err
	}

	if existing != nil {
		// 已存在，更新
		user.ID = existing.ID
		user.CreatedAt = existing.CreatedAt
		return r.Update(ctx, user)
	}

	// 不存在，创建
	return r.Create(ctx, user)
}

// SyncDepartments 同步用户所属部门（事务：先删后插）
func (r *userRepository) SyncDepartments(ctx context.Context, userID uint, deptIDs []int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 删除该用户所有部门关联
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserDepartment{}).Error; err != nil {
			return err
		}

		// 2. 批量插入新关联
		if len(deptIDs) == 0 {
			return nil
		}

		userDepts := make([]model.UserDepartment, 0, len(deptIDs))
		for _, deptID := range deptIDs {
			userDepts = append(userDepts, model.UserDepartment{
				UserID: userID,
				DeptID: uint(deptID),
			})
		}

		return tx.Create(&userDepts).Error
	})
}

// FindDepartmentIDs 查询用户所属部门ID列表
func (r *userRepository) FindDepartmentIDs(ctx context.Context, userID uint) ([]int64, error) {
	var userDepts []model.UserDepartment
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&userDepts).Error; err != nil {
		return nil, err
	}

	deptIDs := make([]int64, 0, len(userDepts))
	for _, ud := range userDepts {
		deptIDs = append(deptIDs, int64(ud.DeptID))
	}

	return deptIDs, nil
}
