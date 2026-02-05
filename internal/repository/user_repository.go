package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"schedule_server/internal/model"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/pinyinutil"

	"gorm.io/gorm"
)

// ErrUserNotFound 预定义错误
var (
	ErrUserNotFound = errors.New("repository: 用户不存在")
)

// UserRepository 用户仓库接口
type UserRepository interface {
	// FindByID 根据ID查询用户
	FindByID(ctx context.Context, id uint) (*model.User, error)
	// FindByDingUserID 根据钉钉用户ID查询用户
	FindByDingUserID(ctx context.Context, dingUserID string) (*model.User, error)
	// ListByIDs 批量查询用户
	ListByIDs(ctx context.Context, ids []uint) ([]model.User, error)
	// ListByScope 按可见范围（部门或指定用户）查询用户
	ListByScope(ctx context.Context, deptIDs []int64, onlyUserIDs []uint) ([]model.User, error)
	// ListByStatus 根据状态查询用户（status=1表示参与考勤）
	ListByStatus(ctx context.Context, status int) ([]model.User, error)
	// List 分页获取本地用户（按ID升序），仅包含已有用户
	List(ctx context.Context, limit, offset int) ([]model.User, error)
	// Search 按关键词分页搜索用户（name/phone/ding_user_id/name_pinyin/name_pinyin_abbr）
	Search(ctx context.Context, keyword string, page, pageSize int) ([]model.User, int, error)
	// SearchWithScope 按关键词分页搜索用户（可按部门或指定用户ID限制）
	SearchWithScope(ctx context.Context, keyword string, deptIDs []int64, onlyUserIDs []uint, page, pageSize int) ([]model.User, int, error)
	// Create 创建用户
	Create(ctx context.Context, user *model.User) error
	// Update 更新用户
	Update(ctx context.Context, user *model.User) error
	// UpdateStatus 更新用户考勤状态
	UpdateStatus(ctx context.Context, userID uint, status int) error
	// UpdateRole 更新用户角色
	UpdateRole(ctx context.Context, userID uint, role int) error
	// Delete 软删除用户
	Delete(ctx context.Context, userID uint) error
	// Upsert 创建或更新用户（根据ding_user_id判断）
	Upsert(ctx context.Context, user *model.User) error
	// SyncDepartments 同步用户所属部门（事务：先删后插）
	SyncDepartments(ctx context.Context, userID uint, deptIDs []int64) error
	// FindDepartmentIDs 查询用户所属部门ID列表
	FindDepartmentIDs(ctx context.Context, userID uint) ([]int64, error)
	// FindDepartments 查询用户所属部门详情
	FindDepartments(ctx context.Context, userID uint) ([]model.Department, error)
	// GetUserDepartmentNames 批量获取用户的部门名称（多个部门用逗号分隔）
	GetUserDepartmentNames(ctx context.Context, userIDs []uint) (map[uint]string, error)
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

// ListByIDs 批量查询用户
func (r *userRepository) ListByIDs(ctx context.Context, ids []uint) ([]model.User, error) {
	if len(ids) == 0 {
		return []model.User{}, nil
	}

	var users []model.User
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// ListByScope 按部门或指定用户ID查询用户（去重）
func (r *userRepository) ListByScope(ctx context.Context, deptIDs []int64, onlyUserIDs []uint) ([]model.User, error) {
	hasDeptIDs := len(deptIDs) > 0
	hasUserIDs := len(onlyUserIDs) > 0

	q := r.db.WithContext(ctx).Model(&model.User{})

	if !hasDeptIDs && !hasUserIDs {
		// 无限制，返回全部
		var users []model.User
		if err := q.Order("id ASC").Find(&users).Error; err != nil {
			return nil, err
		}
		return users, nil
	}

	cleanDeptIDs := make([]int64, 0, len(deptIDs))
	for _, id := range deptIDs {
		if id > 0 {
			cleanDeptIDs = append(cleanDeptIDs, id)
		}
	}
	hasDeptIDs = len(cleanDeptIDs) > 0

	switch {
	case hasDeptIDs && hasUserIDs:
		q = q.Joins("LEFT JOIN user_departments ud ON ud.user_id = users.id AND ud.tenant_id = users.tenant_id").
			Where("(ud.dept_id IN ? OR users.id IN ?)", cleanDeptIDs, onlyUserIDs)
	case hasDeptIDs:
		q = q.Joins("LEFT JOIN user_departments ud ON ud.user_id = users.id AND ud.tenant_id = users.tenant_id").
			Where("ud.dept_id IN ?", cleanDeptIDs)
	case hasUserIDs:
		q = q.Where("users.id IN ?", onlyUserIDs)
	}

	q = q.Distinct("users.id").Order("users.id ASC")

	var users []model.User
	if err := q.Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// List 分页获取本地用户
func (r *userRepository) List(ctx context.Context, limit, offset int) ([]model.User, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var users []model.User
	if err := r.db.WithContext(ctx).
		Order("id ASC").
		Limit(limit).
		Offset(offset).
		Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// Create 创建用户
func (r *userRepository) Create(ctx context.Context, user *model.User) error {
	if user == nil {
		return errors.New("repository: user 为空")
	}
	fillUserPinyin(user)
	return r.db.WithContext(ctx).Create(user).Error
}

// Update 更新用户
func (r *userRepository) Update(ctx context.Context, user *model.User) error {
	if user == nil {
		return errors.New("repository: user 为空")
	}
	fillUserPinyin(user)
	return r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", user.ID).
		Select("*").
		Omit("id").
		Updates(user).Error
}

// UpdateStatus 更新用户考勤状态
func (r *userRepository) UpdateStatus(ctx context.Context, userID uint, status int) error {
	result := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", userID).
		Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UpdateRole 更新用户角色
func (r *userRepository) UpdateRole(ctx context.Context, userID uint, role int) error {
	result := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", userID).
		Update("role", role)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// Delete 软删除用户
func (r *userRepository) Delete(ctx context.Context, userID uint) error {
	result := r.db.WithContext(ctx).Delete(&model.User{}, userID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// Upsert 创建或更新用户（根据ding_user_id判断）
func (r *userRepository) Upsert(ctx context.Context, user *model.User) error {
	if user == nil {
		return errors.New("repository: user 为空")
	}

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

		// 获取 tenant_id
		tenantID, ok := tenantctx.TenantIDFrom(ctx)
		if !ok {
			return fmt.Errorf("context 中缺少 tenant_id")
		}

		userDepts := make([]model.UserDepartment, 0, len(deptIDs))
		for _, deptID := range deptIDs {
			if deptID <= 0 {
				continue
			}
			userDepts = append(userDepts, model.UserDepartment{
				TenantID: tenantID,
				UserID:   userID,
				DeptID:   deptID,
			})
		}

		if len(userDepts) == 0 {
			return nil
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
		deptIDs = append(deptIDs, ud.DeptID)
	}

	return deptIDs, nil
}

// FindDepartments 查询用户所属部门详情
func (r *userRepository) FindDepartments(ctx context.Context, userID uint) ([]model.Department, error) {
	var depts []model.Department
	if err := r.db.WithContext(ctx).
		Model(&model.Department{}).
		Joins("JOIN user_departments ud ON ud.dept_id = departments.dept_id AND ud.tenant_id = departments.tenant_id").
		Where("ud.user_id = ?", userID).
		Find(&depts).Error; err != nil {
		return nil, err
	}
	return depts, nil
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

func applyUserKeywordFilter(q *gorm.DB, keyword string) *gorm.DB {
	if keyword == "" {
		return q
	}

	like := "%" + keyword + "%"
	return q.Where(
		"name LIKE ? OR phone LIKE ? OR ding_user_id LIKE ? OR name_pinyin LIKE ? OR name_pinyin_abbr LIKE ?",
		like, like, like, like, like,
	)
}

// Search 按关键词分页搜索用户
func (r *userRepository) Search(ctx context.Context, keyword string, page, pageSize int) ([]model.User, int, error) {
	q := applyUserKeywordFilter(r.db.WithContext(ctx).Model(&model.User{}), keyword)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []model.User
	if err := q.Order("id DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, int(total), nil
}

// SearchWithScope 按可见范围搜索用户
func (r *userRepository) SearchWithScope(
	ctx context.Context,
	keyword string,
	deptIDs []int64,
	onlyUserIDs []uint,
	page, pageSize int,
) ([]model.User, int, error) {
	hasDeptIDs := len(deptIDs) > 0
	hasUserIDs := len(onlyUserIDs) > 0
	if !hasDeptIDs && !hasUserIDs {
		return r.Search(ctx, keyword, page, pageSize)
	}

	cleanDeptIDs := make([]int64, 0, len(deptIDs))
	for _, id := range deptIDs {
		if id <= 0 {
			continue
		}
		cleanDeptIDs = append(cleanDeptIDs, id)
	}
	hasDeptIDs = len(cleanDeptIDs) > 0

	q := applyUserKeywordFilter(r.db.WithContext(ctx).Model(&model.User{}), keyword)

	switch {
	case hasDeptIDs && hasUserIDs:
		q = q.Joins("LEFT JOIN user_departments ud ON ud.user_id = users.id AND ud.tenant_id = users.tenant_id").
			Where("(ud.dept_id IN ? OR users.id IN ?)", cleanDeptIDs, onlyUserIDs)
	case hasDeptIDs:
		q = q.Joins("LEFT JOIN user_departments ud ON ud.user_id = users.id AND ud.tenant_id = users.tenant_id").
			Where("ud.dept_id IN ?", cleanDeptIDs)
	case hasUserIDs:
		q = q.Where("users.id IN ?", onlyUserIDs)
	}

	q = q.Distinct("users.id")

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

// ListByStatus 根据状态查询用户（status=1表示参与考勤）
func (r *userRepository) ListByStatus(ctx context.Context, status int) ([]model.User, error) {
	var users []model.User
	if err := r.db.WithContext(ctx).
		Where("status = ?", status).
		Order("id ASC").
		Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// GetUserDepartmentNames 批量获取用户的部门名称（多个部门用逗号分隔）
func (r *userRepository) GetUserDepartmentNames(ctx context.Context, userIDs []uint) (map[uint]string, error) {
	if len(userIDs) == 0 {
		return make(map[uint]string), nil
	}

	// 查询用户部门关联及部门信息
	type UserDeptInfo struct {
		UserID   uint
		DeptName string
	}

	var results []UserDeptInfo
	err := r.db.WithContext(ctx).
		Table("user_departments ud").
		Select("ud.user_id, d.name as dept_name").
		Joins("LEFT JOIN departments d ON d.dept_id = ud.dept_id AND d.tenant_id = ud.tenant_id").
		Where("ud.user_id IN ?", userIDs).
		Order("ud.user_id ASC, d.name ASC").
		Scan(&results).Error

	if err != nil {
		return nil, err
	}

	// 构建用户ID到部门名称的映射（多个部门用逗号分隔）
	userDeptMap := make(map[uint][]string)
	for _, r := range results {
		if r.DeptName != "" {
			userDeptMap[r.UserID] = append(userDeptMap[r.UserID], r.DeptName)
		}
	}

	// 将部门列表转换为逗号分隔的字符串
	result := make(map[uint]string)
	for userID, deptNames := range userDeptMap {
		result[userID] = strings.Join(deptNames, ",")
	}

	return result, nil
}
