package handler

import (
	"errors"
	"net/http"

	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	authSrv *service.AuthService
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(authSrv *service.AuthService) *AuthHandler {
	return &AuthHandler{authSrv: authSrv}
}

// LoginRequest 登录请求
type LoginRequest struct {
	AuthCode string `json:"auth_code" binding:"required"`
}

// Login 钉钉免登码登录
// @Summary 用户登录
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body LoginRequest true "登录请求"
// @Success 200 {object} service.LoginResult
// @Router /api/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, "auth_code不能为空")
		return
	}

	result, err := h.authSrv.Login(c.Request.Context(), req.AuthCode)
	if err != nil {
		if errors.Is(err, service.ErrAuthCodeRequired) {
			response.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		response.FailWithError(c, err)
		return
	}

	response.OK(c, result)
}

// GetCurrentUser 获取当前登录用户信息
// @Summary 获取当前用户
// @Tags Auth
// @Security Bearer
// @Success 200 {object} service.LoginUser
// @Router /api/auth/me [get]
func (h *AuthHandler) GetCurrentUser(c *gin.Context) {
	// 从Context中获取用户信息（由JWT中间件写入）
	userID, exists := c.Get("user_id")
	if !exists {
		response.Fail(c, http.StatusUnauthorized, "未登录")
		return
	}

	dingUserID, _ := c.Get("ding_user_id")
	name, _ := c.Get("user_name")

	response.OK(c, gin.H{
		"id":           userID,
		"ding_user_id": dingUserID,
		"name":         name,
	})
}
