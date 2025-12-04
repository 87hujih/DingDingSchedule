package consts

// 用户角色
const (
	RoleMember     = 0 // 普通成员
	RoleGroupLead  = 1 // 小组长
	RoleLabAdmin   = 2 // 实验室管理员
	RoleSuperAdmin = 3 // 超级管理员
)

// RoleNames 角色名称映射
var RoleNames = map[int]string{
	RoleMember:     "普通成员",
	RoleGroupLead:  "小组长",
	RoleLabAdmin:   "实验室管理员",
	RoleSuperAdmin: "超级管理员",
}

// RoleName 获取角色名称
func RoleName(role int) string {
	if name, ok := RoleNames[role]; ok {
		return name
	}
	return "未知角色"
}
