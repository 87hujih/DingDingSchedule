package handler

import (
	"errors"

	"schedule_server/internal/dto"
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

// Login 钉钉免登码登录
func (h *AuthHandler) Login(ctx *gin.Context) {
	var req dto.LoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, response.TranslateValidationError(err))
		return
	}

	result, err := h.authSrv.Login(ctx.Request.Context(), req.AuthCode)
	if err != nil {
		if errors.Is(err, service.ErrAuthCodeRequired) {
			response.Fail(ctx, response.CodeInvalidParam, err.Error())
			return
		}
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}
