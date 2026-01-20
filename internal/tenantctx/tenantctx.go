package tenantctx

import "context"

// ctxKey 使用私有类型，避免 context key 冲突
type ctxKey int

const (
	tenantIDKey ctxKey = iota
	skipTenantScopeKey
)

// WithTenantID 将 tenant_id 写入 context。
func WithTenantID(ctx context.Context, tenantID uint) context.Context {
	return context.WithValue(ctx, tenantIDKey, tenantID)
}

// TenantIDFrom 从 context 中读取 tenant_id。
func TenantIDFrom(ctx context.Context) (uint, bool) {
	v := ctx.Value(tenantIDKey)
	id, ok := v.(uint)
	if !ok || id == 0 {
		return 0, false
	}
	return id, true
}

// WithSkipTenantScope 标记该 context 跳过租户隔离（仅用于迁移/后台任务等少数场景）。
func WithSkipTenantScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipTenantScopeKey, true)
}

// SkipTenantScopeFrom 判断该 context 是否标记为跳过租户隔离。
func SkipTenantScopeFrom(ctx context.Context) bool {
	v := ctx.Value(skipTenantScopeKey)
	b, ok := v.(bool)
	return ok && b
}
