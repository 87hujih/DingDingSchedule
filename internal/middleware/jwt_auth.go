package middleware

import (
	"errors"
	"strings"
	"time"

	"schedule_server/config"
	"schedule_server/internal/response"
	"schedule_server/pkg/jwt"

	"github.com/gin-gonic/gin"
)

const (
	// AuthorizationHeader 认证头
	AuthorizationHeader = "Authorization"
	// BearerPrefix Bearer Token前缀
	BearerPrefix = "Bearer "
)

// ContextKey 上下文键
const (
	CtxKeyUserID     = "user_id"
	CtxKeyDingUserID = "ding_user_id"
	CtxKeyUserName   = "user_name"
)

// 包级变量，存储已初始化的 JWT Manager
var jwtMgr *jwt.Manager

// Init 初始化中间件（应用启动时调用一次）
func Init(jwtCfg config.JWT) {
	expire, _ := time.ParseDuration(jwtCfg.Expire)
	if expire <= 0 {
		expire = 72 * time.Hour
	}

	jwtMgr = jwt.NewManager(jwt.Config{
		Secret: jwtCfg.Secret,
		Expire: expire,
		Issuer: jwtCfg.Issuer,
	})
}

// JWTAuth JWT认证中间件
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 获取Authorization头
		authHeader := c.GetHeader(AuthorizationHeader)
		if authHeader == "" {
			response.New(c).Code(response.CodeUnauthorized).Message("缺少认证信息").Abort()
			return
		}

		// 2. 检查Bearer前缀
		if !strings.HasPrefix(authHeader, BearerPrefix) {
			response.New(c).Code(response.CodeUnauthorized).Message("认证格式错误").Abort()
			return
		}

		// 3. 提取Token
		tokenString := strings.TrimPrefix(authHeader, BearerPrefix)
		if tokenString == "" {
			response.New(c).Code(response.CodeUnauthorized).Message("Token为空").Abort()
			return
		}

		// 4. 解析验证Token
		claims, err := jwtMgr.ParseToken(tokenString)
		if err != nil {
			msg := "Token无效"
			if errors.Is(err, jwt.ErrTokenExpired) {
				msg = "Token已过期"
			}
			response.New(c).Code(response.CodeUnauthorized).Message(msg).Abort()
			return
		}

		// 5. 将用户信息写入Context
		c.Set(CtxKeyUserID, claims.UserID)
		c.Set(CtxKeyDingUserID, claims.DingUserID)
		c.Set(CtxKeyUserName, claims.Name)

		c.Next()
	}
}
