package service

import (
	"context"

	"schedule_server/internal/consts"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
)

// UserScope 描述用户可见范围：按部门或限定用户ID
type UserScope struct {
	DeptIDs     []int64
	OnlyUserIDs []uint
}

// VisibleUserScope 计算当前调用者在用户维度的可见范围
// - 管理员：不限制
// - 普通成员：仅自己
func VisibleUserScope(ctx context.Context, userRepo repository.UserRepository, viewerID uint, viewerRole int) (*UserScope, error) {
	if viewerID == 0 {
		return nil, response.ErrForbidden()
	}

	scope := &UserScope{}

	switch {
	case viewerRole >= consts.RoleAdmin:
		// 不限制
		return scope, nil
	default:
		scope.OnlyUserIDs = []uint{viewerID}
	}

	return scope, nil
}
