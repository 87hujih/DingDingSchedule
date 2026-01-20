package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"schedule_server/config"
	"schedule_server/internal/consts"
	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/jwt"
)

// 预定义错误
var (
	ErrAuthCodeRequired = errors.New("auth: 免登码不能为空")
	ErrLoginFailed      = errors.New("auth: 登录失败")
)

// AuthService 认证服务
type AuthService struct {
	userRepo  repository.UserRepository
	dingMgr   *DingTalkClientManager
	jwtMgr    *jwt.Manager
	jwtExpire time.Duration
}

// NewAuthService 创建认证服务实例
func NewAuthService(
	userRepo repository.UserRepository,
	dingMgr *DingTalkClientManager,
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
		userRepo:  userRepo,
		dingMgr:   dingMgr,
		jwtMgr:    jwtMgr,
		jwtExpire: expire,
	}
}

// Login 钉钉免登码登录
func (s *AuthService) Login(ctx context.Context, corpID string, authCode string) (*dto.LoginResponse, error) {
	if authCode == "" {
		return nil, ErrAuthCodeRequired
	}
	if s.dingMgr == nil {
		return nil, response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}

	tenant, client, err := s.dingMgr.GetByCorpID(ctx, corpID)
	if err != nil {
		if errors.Is(err, repository.ErrTenantNotFound) {
			return nil, response.ErrInvalidParamWithMsg("corp_id 无效或企业未开通")
		}
		return nil, fmt.Errorf("%w: 获取企业配置失败: %v", ErrLoginFailed, err)
	}
	tenantCtx := tenantctx.WithTenantID(ctx, tenant.ID)

	// 通过免登码直接获取用户ID
	userID, err := client.GetUserByAuthCode(ctx, authCode)
	if err != nil {
		return nil, fmt.Errorf("%w: 获取用户ID失败: %v", ErrLoginFailed, err)
	}

	// 获取用户详细信息（包含部门列表）
	userDetail, err := client.GetUserDetail(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: 获取用户详情失败: %v", ErrLoginFailed, err)
	}

	// 创建或更新本地用户
	user := &model.User{
		TenantID:   tenant.ID,
		DingUserID: userDetail.UserID,
		Name:       userDetail.Name,
		Phone:      userDetail.Mobile,
		Avatar:     userDetail.Avatar,
		Status:     1,
	}

	if err := s.userRepo.Upsert(tenantCtx, user); err != nil {
		return nil, fmt.Errorf("%w: 保存用户失败: %v", ErrLoginFailed, err)
	}

	// 同步用户部门关联
	if err := s.userRepo.SyncDepartments(tenantCtx, user.ID, userDetail.DeptIDList); err != nil {
		return nil, fmt.Errorf("%w: 同步部门失败: %v", ErrLoginFailed, err)
	}

	// 签发JWT
	token, err := s.jwtMgr.GenerateTokenWithTenant(tenant.ID, tenant.CorpID, user.ID, user.DingUserID, user.Name, user.Role)
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
			RoleName:   consts.RoleName(user.Role),
			DeptIDs:    userDetail.DeptIDList,
		},
	}, nil
}

// ParseToken 解析Token获取用户信息
func (s *AuthService) ParseToken(tokenString string) (*jwt.Claims, error) {
	return s.jwtMgr.ParseToken(tokenString)
}
