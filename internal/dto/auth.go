package dto

// LoginRequest 登录请求
type LoginRequest struct {
	AuthCode string `json:"auth_code" binding:"required"` // 免登授权码
	CorpID   string `json:"corp_id" binding:"required"`   // 企业标识ID
}

// LoginResponse 登录响应
type LoginResponse struct {
	Token     string     `json:"token"`
	ExpiresIn int64      `json:"expires_in"` // 过期时间（秒）
	User      *LoginUser `json:"user"`
}

// LoginUser 登录用户信息
type LoginUser struct {
	ID         uint    `json:"id"`
	DingUserID string  `json:"ding_user_id"`
	Name       string  `json:"name"`
	Avatar     string  `json:"avatar"`
	Phone      string  `json:"phone"`
	Role       int     `json:"role"`      // 用户角色
	RoleName   string  `json:"role_name"` // 角色名称
	DeptIDs    []int64 `json:"dept_ids"`
}
