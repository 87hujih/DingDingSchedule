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
	CreateAt  time.Time `json:"create_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewGetUserResponse 从 model.User 构造响应
func NewGetUserResponse(u *model.User) *GetUserResponse {
	return &GetUserResponse{
		ID:        u.ID,
		Name:      u.Name,
		Avatar:    u.Avatar,
		Phone:     u.Phone,
		CreateAt:  u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}
