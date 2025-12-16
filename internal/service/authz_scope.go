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
// - 组长：限制在自己所属部门，若未同步部门则至少返回自己
// - 普通成员：仅自己
func VisibleUserScope(ctx context.Context, userRepo repository.UserRepository, viewerID uint, viewerRole int) (*UserScope, error) {
	if viewerID == 0 {
		return nil, response.ErrForbidden()
	}

	scope := &UserScope{}

	switch {
	case viewerRole >= consts.RoleLabAdmin:
		// 不限制
		return scope, nil
	case viewerRole >= consts.RoleGroupLead:
		deptIDs, err := userRepo.FindDepartmentIDs(ctx, viewerID)
		if err != nil {
			return nil, err
		}
		scope.DeptIDs = deptIDs
		if len(deptIDs) == 0 {
			scope.OnlyUserIDs = []uint{viewerID}
		}
	default:
		scope.OnlyUserIDs = []uint{viewerID}
	}

	return scope, nil
}
