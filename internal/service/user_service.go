package service

import (
	"context"
	"strings"

	"schedule_server/internal/consts"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
)

// UserService 用户服务
type UserService struct {
	userRepo repository.UserRepository
}

// SearchUsersResult 用户搜索结果
type SearchUsersResult struct {
	Users    []model.User
	Total    int
	Page     int
	PageSize int
}

// NewUserService 创建用户服务实例
func NewUserService(userRepo repository.UserRepository) *UserService {
	return &UserService{userRepo: userRepo}
}

// GetUserById 获取用户信息
func (s *UserService) GetUserById(ctx context.Context, id uint) (*model.User, error) {
	return s.userRepo.FindByID(ctx, id)
}

// GetUserDeptNames 获取用户部门名称列表
func (s *UserService) GetUserDeptNames(ctx context.Context, id uint) ([]string, error) {
	return s.userRepo.FindDepartmentNames(ctx, id)
}

// SearchUsers 按关键词分页搜索用户
func (s *UserService) SearchUsers(
	ctx context.Context, viewerID uint, keyword string, page, pageSize int,
) (*SearchUsersResult, error) {
	// 仍需登录（中间件已验证），这里兜底防护
	if viewerID == 0 {
		return nil, response.ErrForbidden()
	}

	keyword, page, pageSize = normalizeSearchParams(keyword, page, pageSize)

	users, total, err := s.userRepo.Search(ctx, keyword, page, pageSize, nil, nil)
	if err != nil {
		return nil, err
	}

	return &SearchUsersResult{
		Users:    users,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// SearchUsersWithScope 按可见范围搜索用户（非普通角色）
func (s *UserService) SearchUsersWithScope(
	ctx context.Context, viewerID uint, viewerRole int, keyword string, page, pageSize int,
) (*SearchUsersResult, error) {
	if viewerID == 0 {
		return nil, response.ErrForbidden()
	}
	if viewerRole < consts.RoleGroupLead {
		return nil, response.ErrForbidden()
	}

	keyword, page, pageSize = normalizeSearchParams(keyword, page, pageSize)

	scope, err := VisibleUserScope(ctx, s.userRepo, viewerID, viewerRole)
	if err != nil {
		return nil, err
	}

	users, total, err := s.userRepo.Search(ctx, keyword, page, pageSize, scope.DeptIDs, scope.OnlyUserIDs)
	if err != nil {
		return nil, err
	}

	return &SearchUsersResult{
		Users:    users,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func normalizeSearchParams(keyword string, page, pageSize int) (string, int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	return strings.TrimSpace(keyword), page, pageSize
}

func (s *UserService) GetUserWithScope(ctx context.Context, viewerID uint, viewerRole int, targetID uint) (*model.User, []string, error) {
	if viewerID == 0 || targetID == 0 {
		return nil, nil, response.ErrForbidden()
	}

	// 管理员：直接放行
	if viewerRole >= consts.RoleLabAdmin {
		user, err := s.userRepo.FindByID(ctx, targetID)
		if err != nil {
			return nil, nil, err
		}
		deptNames, err := s.userRepo.FindDepartmentNames(ctx, targetID)
		if err != nil {
			return nil, nil, err
		}
		return user, deptNames, nil
	}

	// 普通成员：直接拒绝
	if viewerRole < consts.RoleGroupLead {
		return nil, nil, response.ErrForbidden()
	}

	// 计算可见范围
	scope, err := VisibleUserScope(ctx, s.userRepo, viewerID, viewerRole)
	if err != nil {
		return nil, nil, err
	}

	// 先检查特定用户白名单（如自己或无部门兜底）
	for _, id := range scope.OnlyUserIDs {
		if id == targetID {
			user, err := s.userRepo.FindByID(ctx, targetID)
			if err != nil {
				return nil, nil, err
			}
			deptNames, err := s.userRepo.FindDepartmentNames(ctx, targetID)
			if err != nil {
				return nil, nil, err
			}
			return user, deptNames, nil
		}
	}

	// 无部门范围可用，直接拒绝
	if len(scope.DeptIDs) == 0 {
		return nil, nil, response.ErrForbidden()
	}

	// 部门交集判断（map 以适配多部门）
	allowed := make(map[int64]struct{}, len(scope.DeptIDs))
	for _, d := range scope.DeptIDs {
		allowed[d] = struct{}{}
	}

	targetDeptIDs, err := s.userRepo.FindDepartmentIDs(ctx, targetID)
	if err != nil {
		return nil, nil, err
	}

	for _, d := range targetDeptIDs {
		if _, ok := allowed[d]; ok {
			user, err := s.userRepo.FindByID(ctx, targetID)
			if err != nil {
				return nil, nil, err
			}
			deptNames, err := s.userRepo.FindDepartmentNames(ctx, targetID)
			if err != nil {
				return nil, nil, err
			}
			return user, deptNames, nil
		}
	}

	return nil, nil, response.ErrForbidden()
}
