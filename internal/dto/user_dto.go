package dto

import "schedule_server/internal/model"

// ===================== 请求 DTO =====================

// UserCreateReq 创建用户请求
type UserCreateReq struct {
	DingUserID string `json:"ding_user_id" binding:"required"`
	Name       string `json:"name" binding:"required"`
	Phone      string `json:"phone"`
	DeptID     uint   `json:"dept_id"`
}

// UserUpdateReq 更新用户请求
type UserUpdateReq struct {
	Name   string `json:"name"`
	Phone  string `json:"phone"`
	DeptID uint   `json:"dept_id"`
}

// ===================== 响应 DTO =====================

// UserDTO 用户响应
type UserDTO struct {
	ID         uint   `json:"id"`
	DingUserID string `json:"ding_user_id"`
	Name       string `json:"name"`
	Phone      string `json:"phone,omitempty"`
	DeptName   string `json:"dept_name,omitempty"`
}

// UserBriefDTO 用户简要信息（用于关联展示）
type UserBriefDTO struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// ===================== 转换函数 =====================

// ToUserDTO model → DTO
func ToUserDTO(u *model.User) *UserDTO {
	dto := &UserDTO{
		ID:         u.ID,
		DingUserID: u.DingUserID,
		Name:       u.Name,
		Phone:      u.Phone,
	}
	// 如果有关联的部门信息
	if u.Dept != nil {
		dto.DeptName = u.Dept.Name
	}
	return dto
}

// ToUserBriefDTO model → 简要 DTO
func ToUserBriefDTO(u *model.User) *UserBriefDTO {
	return &UserBriefDTO{
		ID:   u.ID,
		Name: u.Name,
	}
}
