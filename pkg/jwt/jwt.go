package jwt

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 预定义错误
var (
	ErrTokenExpired     = errors.New("jwt: token已过期")
	ErrTokenInvalid     = errors.New("jwt: token无效")
	ErrTokenMalformed   = errors.New("jwt: token格式错误")
	ErrTokenNotValidYet = errors.New("jwt: token尚未生效")
	ErrClaimsInvalid    = errors.New("jwt: claims类型断言失败")
)

// Claims 自定义JWT载荷
type Claims struct {
	UserID     uint   `json:"user_id"`
	DingUserID string `json:"ding_user_id"`
	Name       string `json:"name"`
	jwt.RegisteredClaims
}

// Config JWT配置
type Config struct {
	Secret string
	Expire time.Duration
	Issuer string
}

// Manager JWT管理器
type Manager struct {
	cfg Config
}

// NewManager 创建JWT管理器
func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg}
}

// GenerateToken 签发Token
func (m *Manager) GenerateToken(userID uint, dingUserID, name string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:     userID,
		DingUserID: dingUserID,
		Name:       name,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.cfg.Issuer,
			Subject:   dingUserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.cfg.Expire)),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(m.cfg.Secret))
}

// ParseToken 解析并验证Token
func (m *Manager) ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		return []byte(m.cfg.Secret), nil
	})
	if err != nil {
		return nil, m.mapError(err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrClaimsInvalid
	}

	return claims, nil
}

// mapError 将jwt库错误映射为自定义错误
func (m *Manager) mapError(err error) error {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return ErrTokenExpired
	case errors.Is(err, jwt.ErrTokenMalformed):
		return ErrTokenMalformed
	case errors.Is(err, jwt.ErrTokenNotValidYet):
		return ErrTokenNotValidYet
	default:
		return ErrTokenInvalid
	}
}
