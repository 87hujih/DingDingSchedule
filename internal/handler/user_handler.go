package handler

import (
	"strconv"

	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// getViewerAuth 从上下文提取用户ID与角色，失败时直接返回响应
func getViewerAuth(ctx *gin.Context) (userID uint, role int, ok bool) {
	userID = ctx.GetUint("user_id")
	roleVal, exists := ctx.Get("user_role")
	if userID == 0 || !exists {
		response.Fail(ctx, response.CodeUnauthorized, "未登录或ID无效")
		return 0, 0, false
	}
	var castOK bool
	if role, castOK = roleVal.(int); !castOK {
		response.Fail(ctx, response.CodeUnauthorized, "用户角色无效")
		return 0, 0, false
	}
	return userID, role, true
}

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
	userID, _, ok := getViewerAuth(ctx)
	if !ok {
		return
	}
	user, err := h.userSvc.GetUserById(ctx.Request.Context(), userID)
	if err != nil {
		response.Fail(ctx, response.CodeUserNotFound, "获取用户信息失败")
		return
	}

	deptNames, err := h.userSvc.GetUserDeptNames(ctx.Request.Context(), userID)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewGetUserResponse(user, deptNames))
}

// List 用户列表
func (h *UserHandler) List(ctx *gin.Context) {
	viewerID, _, ok := getViewerAuth(ctx)
	if !ok {
		return
	}
	keyword := ctx.Query("keyword")
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))

	result, err := h.userSvc.SearchUsers(ctx.Request.Context(), viewerID, keyword, page, pageSize)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewUserListResponse(result.Users, result.Page, result.PageSize, result.Total))
}

// ListVisible 用户可见范围内的列表（非普通角色）
func (h *UserHandler) ListVisible(ctx *gin.Context) {
	viewerID, viewerRole, ok := getViewerAuth(ctx)
	if !ok {
		return
	}
	keyword := ctx.Query("keyword")
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))

	result, err := h.userSvc.SearchUsersWithScope(ctx.Request.Context(), viewerID, viewerRole, keyword, page, pageSize)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewUserListResponse(result.Users, result.Page, result.PageSize, result.Total))
}

// GetUser 获取指定用户（需非普通角色且在可见范围内）
func (h *UserHandler) GetUser(ctx *gin.Context) {
	viewerID, viewerRole, ok := getViewerAuth(ctx)
	if !ok {
		return
	}

	targetIDUint, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || targetIDUint == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "用户ID无效")
		return
	}

	user, deptNames, err := h.userSvc.GetUserWithScope(ctx.Request.Context(), viewerID, viewerRole, uint(targetIDUint))
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewGetUserResponse(user, deptNames))
}
