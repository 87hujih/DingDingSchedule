package handler

import (
	"net/http"

	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// UserHandler 用户处理器
type UserHandler struct {
	userSvc *service.UserService
}

// NewUserHandler 创建用户处理器
func NewUserHandler(userSvc *service.UserService) *UserHandler {
	return &UserHandler{userSvc: userSvc}
}

// GetCurrentUser 获取当前登录用户信息
func (h *UserHandler) GetCurrentUser(ctx *gin.Context) {
	uid := ctx.GetUint("user_id")
	if uid == 0 {
		response.Fail(ctx, response.CodeUnauthorized, "未登录或ID无效")
		return
	}
	user, err := h.userSvc.GetUserById(ctx.Request.Context(), uid)
	if err != nil {
		response.Fail(ctx, response.CodeUserNotFound, "获取用户信息失败")
		return
	}

	response.OK(ctx, dto.NewGetUserResponse(user))
}

// Create 创建用户
func (h *UserHandler) Create(ctx *gin.Context) {
	// TODO: 实现创建用户逻辑
	ctx.JSON(http.StatusOK, gin.H{"message": "create user"})
}

// List 用户列表
func (h *UserHandler) List(ctx *gin.Context) {
	// TODO: 实现用户列表逻辑
	ctx.JSON(http.StatusOK, gin.H{"message": "list users"})
}
