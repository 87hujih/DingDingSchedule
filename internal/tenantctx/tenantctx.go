package tenantctx

import "context"

// ctxKey 使用私有类型，避免 context key 冲突
type ctxKey int

const tenantIDKey ctxKey = iota

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
