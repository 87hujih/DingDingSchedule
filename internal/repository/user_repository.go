package repository

import (
	"context"
	"errors"
	"strings"

	"schedule_server/internal/model"
	"schedule_server/pkg/pinyinutil"

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
	// Search 按关键词分页搜索用户，可选按部门或用户ID过滤（union）
	// - keyword 匹配 name/phone/ding_user_id/name_pinyin/name_pinyin_abbr
	// - deptIDs: 允许的部门列表（为空则不限制）
	// - onlyUserIDs: 允许的用户ID列表（为空则不限制）
	Search(ctx context.Context, keyword string, page, pageSize int,
		deptIDs []int64, onlyUserIDs []uint) ([]model.User, int, error)
	// FindDepartmentNames 查询用户所属部门名称列表
	FindDepartmentNames(ctx context.Context, userID uint) ([]string, error)
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
	fillUserPinyin(user)
	return r.db.WithContext(ctx).Create(user).Error
}

// Update 更新用户
func (r *userRepository) Update(ctx context.Context, user *model.User) error {
	fillUserPinyin(user)
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
		user.Role = existing.Role
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

// FindDepartmentNames 查询用户所属部门名称列表
func (r *userRepository) FindDepartmentNames(ctx context.Context, userID uint) ([]string, error) {
	var names []string
	if err := r.db.WithContext(ctx).
		Table("user_departments AS ud").
		Select("d.name").
		Joins("JOIN departments d ON d.dept_id = ud.dept_id").
		Where("ud.user_id = ?", userID).
		Scan(&names).Error; err != nil {
		return nil, err
	}
	return names, nil
}

func fillUserPinyin(user *model.User) {
	if user == nil {
		return
	}
	if strings.TrimSpace(user.Name) == "" {
		user.NamePinyin = ""
		user.NamePinyinAbbr = ""
		return
	}

	full, abbr := pinyinutil.FullAndAbbr(user.Name)
	user.NamePinyin = full
	user.NamePinyinAbbr = abbr
}

// Search 按关键词分页搜索用户
func (r *userRepository) Search(
	ctx context.Context,
	keyword string,
	page,
	pageSize int,
	deptIDs []int64,
	onlyUserIDs []uint,
) ([]model.User, int, error) {
	q := r.db.WithContext(ctx).Model(&model.User{}).Distinct("users.id")

	useDept := len(deptIDs) > 0
	useUserIDs := len(onlyUserIDs) > 0

	if useDept {
		q = q.Joins("JOIN user_departments ud ON ud.user_id = users.id")
	}

	switch {
	case useDept && useUserIDs:
		q = q.Where(r.db.Where("users.id IN ?", onlyUserIDs).Or("ud.dept_id IN ?", deptIDs))
	case useDept:
		q = q.Where("ud.dept_id IN ?", deptIDs)
	case useUserIDs:
		q = q.Where("users.id IN ?", onlyUserIDs)
	}

	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where(
			"name LIKE ? OR phone LIKE ? OR ding_user_id LIKE ? OR name_pinyin LIKE ? OR name_pinyin_abbr LIKE ?",
			like, like, like, like, like,
		)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []model.User
	if err := q.Order("users.id DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, int(total), nil
}
