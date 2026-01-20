package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
	"schedule_server/pkg/dingtalk"
)

// UserService 用户服务
type UserService struct {
	userRepo repository.UserRepository
	dingMgr  *DingTalkClientManager
}

// SearchUsersResult 用户搜索结果
type SearchUsersResult struct {
	Users    []model.User
	Total    int
	Page     int
	PageSize int
}

// RefreshUserResult 刷新用户信息结果
type RefreshUserResult struct {
	User    *model.User
	DeptIDs []int64
}

// NewUserService 创建用户服务实例
func NewUserService(userRepo repository.UserRepository, dingMgr *DingTalkClientManager) *UserService {
	return &UserService{
		userRepo: userRepo,
		dingMgr:  dingMgr,
	}
}

// GetUserById 获取用户信息
func (s *UserService) GetUserById(ctx context.Context, id uint) (*model.User, error) {
	return s.userRepo.FindByID(ctx, id)
}

// GetUserWithDepts 获取用户及部门列表
func (s *UserService) GetUserWithDepts(ctx context.Context, id uint) (*model.User, []model.Department, error) {
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	depts, err := s.userRepo.FindDepartments(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	return user, depts, nil
}

// GetVisibleUser 获取指定用户信息（按角色范围限制）
func (s *UserService) GetVisibleUser(
	ctx context.Context, viewerID uint, targetUserID uint,
) (*model.User, []model.Department, error) {
	if viewerID == 0 || targetUserID == 0 {
		return nil, nil, response.ErrForbidden()
	}
	return s.GetUserWithDepts(ctx, targetUserID)
}

// 规范分页参数
func normalizeUserSearchPagination(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	return page, pageSize
}

// SearchUsers 按关键词分页搜索用户
func (s *UserService) SearchUsers(
	ctx context.Context, keyword string, page, pageSize int,
) (*SearchUsersResult, error) {
	page, pageSize = normalizeUserSearchPagination(page, pageSize)
	keyword = strings.TrimSpace(keyword)

	users, total, err := s.userRepo.Search(ctx, keyword, page, pageSize)
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

// 两个部门是否存在交集
func deptHasIntersection(a, b []int64) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	set := make(map[int64]struct{}, len(a))
	for _, id := range a {
		set[id] = struct{}{}
	}

	for _, id := range b {
		if _, ok := set[id]; ok {
			return true
		}
	}
	return false
}

// SearchVisibleUsers 按可见范围搜索用户（组长仅限所属部门）
func (s *UserService) SearchVisibleUsers(
	ctx context.Context, viewerID uint, viewerRole int, keyword string, page, pageSize int,
) (*SearchUsersResult, error) {
	page, pageSize = normalizeUserSearchPagination(page, pageSize)
	keyword = strings.TrimSpace(keyword)

	scope, err := VisibleUserScope(ctx, s.userRepo, viewerID, viewerRole)
	if err != nil {
		return nil, err
	}

	users, total, err := s.userRepo.SearchWithScope(ctx, keyword, scope.DeptIDs, scope.OnlyUserIDs, page, pageSize)
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

// Refresh 刷新用户信息（从钉钉获取最新信息并更新本地）
func (s *UserService) Refresh(ctx context.Context, dingUserID string) (*RefreshUserResult, error) {
	dingUserID = strings.TrimSpace(dingUserID)
	if dingUserID == "" {
		return nil, response.ErrForbidden()
	}
	if s.dingMgr == nil {
		return nil, response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}

	_, dingClient, err := s.dingMgr.FromContext(ctx)
	if err != nil {
		return nil, response.NewBizError(response.CodeUnauthorized, "缺少租户信息")
	}

	// 获取钉钉用户详情
	detail, err := dingClient.GetUserDetail(ctx, dingUserID)
	if err != nil {
		if errors.Is(err, dingtalk.ErrUserNotFound) {
			return nil, response.ErrDingUserNotFound()
		}
		return nil, fmt.Errorf("获取钉钉用户详情失败: %w", err)
	}

	// 仅更新本地已存在的用户
	user, err := s.userRepo.FindByDingUserID(ctx, dingUserID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, response.ErrUserNotFound()
		}
		return nil, fmt.Errorf("查询本地用户失败: %w", err)
	}

	// 更新基础信息（保留角色等本地字段）
	user.Name = detail.Name
	user.Phone = detail.Mobile
	user.Avatar = detail.Avatar
	user.Status = 1

	if err := s.userRepo.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("更新用户失败: %w", err)
	}

	// 同步部门
	if err := s.userRepo.SyncDepartments(ctx, user.ID, detail.DeptIDList); err != nil {
		return nil, fmt.Errorf("同步部门失败: %w", err)
	}

	return &RefreshUserResult{
		User:    user,
		DeptIDs: detail.DeptIDList,
	}, nil
}

// BatchRefresh 批量刷新（仅刷新本地已有用户）
func (s *UserService) BatchRefresh(ctx context.Context, limit, offset int) (*dto.SyncAllUsersResponse, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	users, err := s.userRepo.List(ctx, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("获取用户列表失败: %w", err)
	}

	totalRequested := len(users)
	hasMore := len(users) == limit
	nextOffset := offset + len(users)

	var successCount int
	failed := make([]dto.SyncAllUsersFailedItem, 0)

	for _, u := range users {
		if _, err := s.Refresh(ctx, u.DingUserID); err != nil {
			failed = append(failed, dto.SyncAllUsersFailedItem{
				DingUserID: u.DingUserID,
				Error:      err.Error(),
			})
			continue
		}
		successCount++
	}

	return dto.NewSyncAllUsersResponse(totalRequested, successCount, failed, hasMore, nextOffset), nil
}

// UpdateUserStatus 更新用户考勤状态
func (s *UserService) UpdateUserStatus(ctx context.Context, targetUserID uint, status int) error {
	if targetUserID == 0 {
		return response.ErrInvalidParam()
	}

	if status != 0 && status != 1 {
		return response.ErrInvalidParamWithMsg("状态值无效")
	}

	if err := s.userRepo.UpdateStatus(ctx, targetUserID, status); err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return response.ErrUserNotFound()
		}
		return fmt.Errorf("更新用户状态失败: %w", err)
	}

	return nil
}

// DeleteUser 删除用户（软删除）
func (s *UserService) DeleteUser(ctx context.Context, targetUserID uint) error {
	if targetUserID == 0 {
		return response.ErrInvalidParam()
	}

	if err := s.userRepo.Delete(ctx, targetUserID); err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return response.ErrUserNotFound()
		}
		return fmt.Errorf("删除用户失败: %w", err)
	}

	return nil
}
