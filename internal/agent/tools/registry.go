package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// UserContext 工具执行时携带的调用者身份
type UserContext struct {
	TenantID          uint
	UserID            uint
	UserRole          int
	DingUserID        string
	Name              string
	ConversationType  string // "1"=单聊, "2"=群聊
	ConversationID    string
	ConversationTitle string
}

// ToolHandler 工具处理函数
type ToolHandler func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error)

// ToolEntry 注册的工具条目
type ToolEntry struct {
	Def     ToolDef
	MinRole int
	Handler ToolHandler
}

// Registry 工具注册表
type Registry struct {
	entries []ToolEntry
	byName  map[string]ToolEntry
}

// NewRegistry 创建工具注册表
func NewRegistry() *Registry {
	return &Registry{
		byName: make(map[string]ToolEntry),
	}
}

// Register 注册工具
func (r *Registry) Register(def ToolDef, minRole int, handler ToolHandler) {
	entry := ToolEntry{Def: def, MinRole: minRole, Handler: handler}
	r.entries = append(r.entries, entry)
	r.byName[def.Function.Name] = entry
}

// ToToolDefs 根据用户角色过滤，返回该用户可用的工具定义列表
func (r *Registry) ToToolDefs(userRole int) []ToolDef {
	var defs []ToolDef
	for _, entry := range r.entries {
		if userRole >= entry.MinRole {
			defs = append(defs, entry.Def)
		}
	}
	return defs
}

// Dispatch 分发工具调用（含二次权限校验）
func (r *Registry) Dispatch(ctx context.Context, uctx *UserContext, name string, params json.RawMessage) (string, error) {
	entry, ok := r.byName[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	if uctx.UserRole < entry.MinRole {
		return `{"error": "权限不足，该功能仅管理员可用"}`, nil
	}
	return entry.Handler(ctx, uctx, params)
}
