package dto

import (
	"schedule_server/internal/consts"
	"schedule_server/internal/model"
	"time"
)

// TenantInfo 租户简要信息
type TenantInfo struct {
	ID     uint   `json:"id"`
	CorpID string `json:"corp_id"`
	Name   string `json:"name"`
}

// GetUserResponse 获取用户信息响应
type GetUserResponse struct {
	ID        uint             `json:"id"`
	Name      string           `json:"name"`
	Role      string           `json:"role"`
	Status    int              `json:"status"`
	Avatar    string           `json:"avatar"`
	Phone     string           `json:"phone"`
	Depts     []DepartmentItem `json:"departments"`
	Tenant    *TenantInfo      `json:"tenant,omitempty"`
	CreateAt  time.Time        `json:"create_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// NewGetUserResponse 从 model.User 构造响应
func NewGetUserResponse(u *model.User, depts []model.Department) *GetUserResponse {
	return NewGetUserResponseWithTenant(u, depts, nil)
}

// NewGetUserResponseWithTenant 从 model.User 构造响应（包含租户信息）
func NewGetUserResponseWithTenant(u *model.User, depts []model.Department, tenant *model.Tenant) *GetUserResponse {
	items := make([]DepartmentItem, 0, len(depts))
	for _, d := range depts {
		items = append(items, DepartmentItem{
			DeptID:   d.DeptID,
			Name:     d.Name,
			ParentID: d.ParentID,
		})
	}

	resp := &GetUserResponse{
		ID:        u.ID,
		Name:      u.Name,
		Role:      consts.RoleName(u.Role),
		Status:    u.Status,
		Avatar:    u.Avatar,
		Phone:     u.Phone,
		Depts:     items,
		CreateAt:  u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}

	if tenant != nil {
		resp.Tenant = &TenantInfo{
			ID:     tenant.ID,
			CorpID: tenant.CorpID,
			Name:   tenant.Name,
		}
	}

	return resp
}

// UserListItem 用户搜索列表项
type UserListItem struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
	Phone  string `json:"phone"`
}

// UserListResponse 用户搜索列表响应
type UserListResponse struct {
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int            `json:"total"`
	Items    []UserListItem `json:"items"`
}

// NewUserListResponse 构造用户列表响应
func NewUserListResponse(users []model.User, page, pageSize, total int) *UserListResponse {
	items := make([]UserListItem, 0, len(users))
	for _, u := range users {
		items = append(items, UserListItem{
			ID:     u.ID,
			Name:   u.Name,
			Avatar: u.Avatar,
			Phone:  u.Phone,
		})
	}

	return &UserListResponse{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
		Items:    items,
	}
}

// SyncAllUsersRequest 批量同步请求
type SyncAllUsersRequest struct {
	Limit  int `json:"limit"`  // 每次刷新数量，默认100
	Offset int `json:"offset"` // 起始偏移
}

// SyncAllUsersFailedItem 失败项
type SyncAllUsersFailedItem struct {
	DingUserID string `json:"ding_user_id"`
	Error      string `json:"error"`
}

// SyncAllUsersResponse 批量同步响应
type SyncAllUsersResponse struct {
	TotalRequested int                      `json:"total_requested"`
	Success        int                      `json:"success"`
	FailedCount    int                      `json:"failed_count"`
	Failed         []SyncAllUsersFailedItem `json:"failed"`
	HasMore        bool                     `json:"has_more"`
	NextOffset     int                      `json:"next_offset"`
}

// NewSyncAllUsersResponse 构造批量同步响应
func NewSyncAllUsersResponse(totalRequested, successCount int, failed []SyncAllUsersFailedItem, hasMore bool, nextOffset int) *SyncAllUsersResponse {
	return &SyncAllUsersResponse{
		TotalRequested: totalRequested,
		Success:        successCount,
		FailedCount:    len(failed),
		Failed:         failed,
		HasMore:        hasMore,
		NextOffset:     nextOffset,
	}
}

// UpdateUserStatusRequest 更新用户考勤状态请求
type UpdateUserStatusRequest struct {
	Status *int `json:"status" binding:"required,oneof=0 1"` // 用户状态(0:不参与;1:参与)
}

// UpdateUserRoleRequest 更新用户角色请求
type UpdateUserRoleRequest struct {
	Role *int `json:"role" binding:"required,oneof=0 1"` // 用户角色(0:普通用户;1:管理员)
}
