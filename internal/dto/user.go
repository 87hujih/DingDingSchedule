package dto

import (
	"schedule_server/internal/model"
	"time"
)

// GetUserResponse 获取用户信息响应
type GetUserResponse struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Avatar    string    `json:"avatar"`
	Phone     string    `json:"phone"`
	DeptNames []string  `json:"dept_names"`
	CreateAt  time.Time `json:"create_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewGetUserResponse 从 model.User 构造响应
func NewGetUserResponse(u *model.User, deptNames []string) *GetUserResponse {
	return &GetUserResponse{
		ID:        u.ID,
		Name:      u.Name,
		Avatar:    u.Avatar,
		Phone:     u.Phone,
		DeptNames: deptNames,
		CreateAt:  u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
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
