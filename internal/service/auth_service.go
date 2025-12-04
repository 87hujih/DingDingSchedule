package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"schedule_server/config"
	"schedule_server/internal/dto"
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
func (s *AuthService) Login(ctx context.Context, authCode string) (*dto.LoginResponse, error) {
	if authCode == "" {
		return nil, ErrAuthCodeRequired
	}

	// 1. 通过免登码直接获取用户ID
	userID, err := s.dingClient.GetUserByAuthCode(ctx, authCode)
	if err != nil {
		return nil, fmt.Errorf("%w: 获取用户ID失败: %v", ErrLoginFailed, err)
	}

	// 2. 获取用户详细信息（包含部门列表）
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
		Status:     1,
	}

	if err := s.userRepo.Upsert(ctx, user); err != nil {
		return nil, fmt.Errorf("%w: 保存用户失败: %v", ErrLoginFailed, err)
	}

	// 5. 同步用户部门关联
	if err := s.userRepo.SyncDepartments(ctx, user.ID, userDetail.DeptIDList); err != nil {
		return nil, fmt.Errorf("%w: 同步部门失败: %v", ErrLoginFailed, err)
	}

	// 6. 签发JWT（包含用户角色信息）
	token, err := s.jwtMgr.GenerateToken(user.ID, user.DingUserID, user.Name, user.Role)
	if err != nil {
		return nil, fmt.Errorf("%w: 签发Token失败: %v", ErrLoginFailed, err)
	}

	return &dto.LoginResponse{
		Token:     token,
		ExpiresIn: int64(s.jwtExpire.Seconds()),
		User: &dto.LoginUser{
			ID:         user.ID,
			DingUserID: user.DingUserID,
			Name:       user.Name,
			Avatar:     user.Avatar,
			Phone:      user.Phone,
			Role:       user.Role,
			RoleName:   user.RoleName(),
			DeptIDs:    userDetail.DeptIDList,
		},
	}, nil
}

// ParseToken 解析Token获取用户信息
func (s *AuthService) ParseToken(tokenString string) (*jwt.Claims, error) {
	return s.jwtMgr.ParseToken(tokenString)
}
