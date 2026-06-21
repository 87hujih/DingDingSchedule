package repository

import (
	"context"
	"errors"

	"schedule_server/internal/tenantctx"
)

var (
	// ErrTenantMissing 请求上下文中缺少 tenant_id（单库多租户模式必须）
	ErrTenantMissing = errors.New("tenant_id缺失")
)

func tenantIDFromCtx(ctx context.Context) (uint, error) {
	id, ok := tenantctx.TenantIDFrom(ctx)
	if !ok || id == 0 {
		return 0, ErrTenantMissing
	}
	return id, nil
}
