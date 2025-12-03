package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"schedule_server/config"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/pkg/dingtalk"
	"schedule_server/pkg/jwt"
)

// 预定义错误
var (
	ErrAuthCodeRequired = errors.New("auth: 免登码不能为空")
	ErrLoginFailed      = errors.New("auth: 登录失败")
)

// LoginResult 登录结果
type LoginResult struct {
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
	DeptIDs    []int64 `json:"dept_ids"`
}

// AuthService 认证服务
type AuthService struct {
	userRepo   repository.UserRepository
	dingClient *dingtalk.Client
	jwtMgr     *jwt.Manager
	jwtExpire  time.Duration
}

// NewAuthService 创建认证服务实例
func NewAuthService(
	userRepo repository.UserRepository,
	dingClient *dingtalk.Client,
	jwtCfg config.JWT,
) *AuthService {
	expire, _ := time.ParseDuration(jwtCfg.Expire)
	if expire <= 0 {
		expire = 72 * time.Hour // 默认72小时
	}

	jwtMgr := jwt.NewManager(jwt.Config{
		Secret: jwtCfg.Secret,
		Expire: expire,
		Issuer: jwtCfg.Issuer,
	})

	return &AuthService{
		userRepo:   userRepo,
		dingClient: dingClient,
		jwtMgr:     jwtMgr,
		jwtExpire:  expire,
	}
}

// Login 钉钉免登码登录
func (s *AuthService) Login(ctx context.Context, authCode string) (*LoginResult, error) {
	if authCode == "" {
		return nil, ErrAuthCodeRequired
	}

	// 1. 通过免登码获取用户基本信息
	userInfo, err := s.dingClient.GetUserByAuthCode(ctx, authCode)
	if err != nil {
		return nil, fmt.Errorf("%w: 获取用户信息失败: %v", ErrLoginFailed, err)
	}

	// 2. 通过unionID获取用户ID
	userID, err := s.dingClient.GetUserIDByUnionID(ctx, userInfo.UnionID)
	if err != nil {
		return nil, fmt.Errorf("%w: 获取用户ID失败: %v", ErrLoginFailed, err)
	}

	// 3. 获取用户详细信息（包含部门列表）
	userDetail, err := s.dingClient.GetUserDetail(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: 获取用户详情失败: %v", ErrLoginFailed, err)
	}

	// 4. 创建或更新本地用户
	user := &model.User{
		DingUserID: userDetail.UserID,
		Name:       userDetail.Name,
		Phone:      userDetail.Mobile,
		Avatar:     userDetail.Avatar,
		Status:     1, // 默认参与考勤
	}

	if err := s.userRepo.Upsert(ctx, user); err != nil {
		return nil, fmt.Errorf("%w: 保存用户失败: %v", ErrLoginFailed, err)
	}

	// 5. 同步用户部门关联
	if err := s.userRepo.SyncDepartments(ctx, user.ID, userDetail.DeptIDList); err != nil {
		return nil, fmt.Errorf("%w: 同步部门失败: %v", ErrLoginFailed, err)
	}

	// 6. 签发JWT
	token, err := s.jwtMgr.GenerateToken(user.ID, user.DingUserID, user.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: 签发Token失败: %v", ErrLoginFailed, err)
	}

	return &LoginResult{
		Token:     token,
		ExpiresIn: int64(s.jwtExpire.Seconds()),
		User: &LoginUser{
			ID:         user.ID,
			DingUserID: user.DingUserID,
			Name:       user.Name,
			Avatar:     user.Avatar,
			Phone:      user.Phone,
			DeptIDs:    userDetail.DeptIDList,
		},
	}, nil
}

// ParseToken 解析Token获取用户信息
func (s *AuthService) ParseToken(tokenString string) (*jwt.Claims, error) {
	return s.jwtMgr.ParseToken(tokenString)
}
