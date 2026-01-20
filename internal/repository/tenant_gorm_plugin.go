package repository

import (
	"context"
	"reflect"

	"schedule_server/internal/tenantctx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const tenantDBName = "tenant_id"

// TenantScopePlugin 用于单库多租户模式的自动租户隔离。
//
// 行为说明：
// - 仅对包含 `tenant_id` 字段的模型生效：
//   - Query/Update/Delete：自动追加 `WHERE <当前表>.tenant_id = <tenantID>`（tenantID 从 ctx 获取）
//   - Create/Update：强制写入/覆盖 `tenant_id = <tenantID>`（防止跨租户写入）
//
// - 若 ctx 缺少 tenant_id：返回 ErrTenantMissing
// - 若 ctx 通过 tenantctx.WithSkipTenantScope 标记跳过：不做任何租户隔离（仅用于迁移/后台任务等少数场景）
type TenantScopePlugin struct{}

// NewTenantScopePlugin 创建一个租户作用域插件实例。
func NewTenantScopePlugin() *TenantScopePlugin { return &TenantScopePlugin{} }

// Name 返回插件在 GORM 中注册使用的名称。
func (*TenantScopePlugin) Name() string { return "tenant_scope" }

// Initialize 在 GORM 的 CRUD 生命周期中注册租户隔离回调。
func (*TenantScopePlugin) Initialize(db *gorm.DB) error {
	// 查询
	if err := db.Callback().Query().Before("gorm:query").Register("tenant_scope:query", tenantScopeQuery); err != nil {
		return err
	}
	// 新增
	if err := db.Callback().Create().Before("gorm:create").Register("tenant_scope:create", tenantScopeCreate); err != nil {
		return err
	}
	// 更新
	if err := db.Callback().Update().Before("gorm:update").Register("tenant_scope:update", tenantScopeUpdate); err != nil {
		return err
	}
	// 删除
	if err := db.Callback().Delete().Before("gorm:delete").Register("tenant_scope:delete", tenantScopeDelete); err != nil {
		return err
	}
	return nil
}

// tenantScopeQuery 在执行查询前注入租户过滤条件。
func tenantScopeQuery(db *gorm.DB) { tenantScopeWhere(db) }

// tenantScopeDelete 在删除语句执行前限定当前租户。
func tenantScopeDelete(db *gorm.DB) { tenantScopeWhere(db) }

// tenantScopeCreate 在创建记录前将上下文中的 tenant_id 写入数据。
func tenantScopeCreate(db *gorm.DB) {
	tenantID, ok := tenantIDForStatement(db)
	if !ok {
		return
	}

	stmt := db.Statement
	if stmt == nil || stmt.Schema == nil {
		return
	}

	tenantField := tenantFieldFromSchema(stmt.Schema)
	if tenantField == nil {
		return
	}

	if err := forceSetTenantID(stmt.Context, tenantField, stmt.ReflectValue, tenantID); err != nil {
		db.AddError(err)
		return
	}
}

// tenantScopeUpdate 在更新操作中追加租户条件并强制覆盖 tenant_id。
func tenantScopeUpdate(db *gorm.DB) {
	tenantID, ok := tenantIDForStatement(db)
	if !ok {
		return
	}

	stmt := db.Statement
	if stmt == nil || stmt.Schema == nil {
		return
	}

	tenantField := tenantFieldFromSchema(stmt.Schema)
	if tenantField == nil {
		return
	}

	// 1) 确保 UPDATE 必然按 tenant_id 进行隔离
	tenantScopeWhere(db)

	// 2) 强制写入 tenant_id，避免通过 Updates/Save 等路径发生跨租户写入。
	// 正常情况下写入同样的值；如有恶意/错误输入，则会被覆盖为 ctx 中的 tenantID。
	stmt.SetColumn(tenantDBName, tenantID)

	// 同时尝试写入到 struct（覆盖部分更新场景）。
	_ = forceSetTenantID(stmt.Context, tenantField, stmt.ReflectValue, tenantID)
}

// tenantScopeWhere 在当前 statement 的模型包含 tenant_id 时，注入 `WHERE <当前表>.tenant_id = ?`。
func tenantScopeWhere(db *gorm.DB) {
	tenantID, ok := tenantIDForStatement(db)
	if !ok {
		return
	}

	stmt := db.Statement
	if stmt == nil || stmt.Schema == nil {
		return
	}

	if tenantFieldFromSchema(stmt.Schema) == nil {
		return
	}

	db.Statement.AddClause(clause.Where{
		Exprs: []clause.Expression{
			clause.Eq{
				Column: clause.Column{Table: clause.CurrentTable, Name: tenantDBName},
				Value:  tenantID,
			},
		},
	})
}

// tenantIDForStatement 从当前 GORM 语句决定是否需要租户隔离并返回 tenant_id。
func tenantIDForStatement(db *gorm.DB) (uint, bool) {
	if db == nil || db.Error != nil {
		return 0, false
	}
	stmt := db.Statement
	if stmt == nil {
		return 0, false
	}
	// 仅当当前模型包含 tenant_id 字段时，才需要从 ctx 读取 tenant 并执行租户隔离。
	// 这样 tenants/user_types 等非租户表不会被误伤（例如登录阶段查租户配置时 ctx 尚未有 tenant_id）。
	if stmt.Schema == nil || tenantFieldFromSchema(stmt.Schema) == nil {
		return 0, false
	}
	ctx := stmt.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if tenantctx.SkipTenantScopeFrom(ctx) {
		return 0, false
	}
	tenantID, err := tenantIDFromCtx(ctx)
	if err != nil {
		db.AddError(err)
		return 0, false
	}
	return tenantID, true
}

// tenantFieldFromSchema 在 schema 中查找 tenant_id 所对应的字段。
func tenantFieldFromSchema(s *schema.Schema) *schema.Field {
	if s == nil {
		return nil
	}
	if f, ok := s.FieldsByDBName[tenantDBName]; ok {
		return f
	}
	return nil
}

// forceSetTenantID 在模型值（结构体或集合）中递归写入指定 tenant_id。
func forceSetTenantID(ctx context.Context, tenantField *schema.Field, v reflect.Value, tenantID uint) error {
	if !v.IsValid() || tenantField == nil {
		return nil
	}

	// 解引用指针
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Struct:
		return tenantField.Set(ctx, v, tenantID)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			for elem.Kind() == reflect.Ptr {
				if elem.IsNil() {
					break
				}
				elem = elem.Elem()
			}
			if !elem.IsValid() || elem.Kind() != reflect.Struct {
				continue
			}
			if err := tenantField.Set(ctx, elem, tenantID); err != nil {
				return err
			}
		}
	}
	return nil
}
