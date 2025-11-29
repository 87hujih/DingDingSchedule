package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// UserServicer 用户服务接口，用于依赖注入和测试 mock
type UserServicer interface {
	// 随着业务增长，在此添加方法签名
}

// UserHandler 用户处理器
type UserHandler struct {
	userSvc UserServicer
}

// NewUserHandler 创建用户处理器
func NewUserHandler(userSvc UserServicer) *UserHandler {
	return &UserHandler{userSvc: userSvc}
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
