package consts

// 用户角色
const (
	RoleUser       = 0 // 普通用户
	RoleAdmin      = 1 // 管理员
	RoleSuperAdmin = 2 // 超级管理员
)

// RoleNames 角色名称映射
var RoleNames = map[int]string{
	RoleUser:       "普通用户",
	RoleAdmin:      "管理员",
	RoleSuperAdmin: "超级管理员",
}

// RoleName 获取角色名称
func RoleName(role int) string {
	if name, ok := RoleNames[role]; ok {
		return name
	}
	return "未知角色"
}
