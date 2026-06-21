package handler

import (
	"errors"
	"io"
	"strconv"
	"strings"

	"schedule_server/internal/consts"
	"schedule_server/internal/dto"
	"schedule_server/internal/middleware"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
	"schedule_server/internal/service"
	"schedule_server/internal/tenantctx"

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
	userSvc    *service.UserService
	tenantRepo repository.TenantRepository
}

// NewUserHandler 创建用户处理器
func NewUserHandler(userSvc *service.UserService, tenantRepo repository.TenantRepository) *UserHandler {
	return &UserHandler{userSvc: userSvc, tenantRepo: tenantRepo}
}

// GetCurrentUser 获取当前登录用户信息
func (h *UserHandler) GetCurrentUser(ctx *gin.Context) {
	userID, _, ok := getViewerAuth(ctx)
	if !ok {
		return
	}
	user, depts, err := h.userSvc.GetUserWithDepts(ctx.Request.Context(), userID)
	if err != nil {
		response.Fail(ctx, response.CodeUserNotFound, "获取用户信息失败")
		return
	}

	// 获取租户信息
	var tenant *model.Tenant
	if tenantID, ok := tenantctx.TenantIDFrom(ctx.Request.Context()); ok {
		tenant, _ = h.tenantRepo.FindByID(ctx.Request.Context(), tenantID)
	}

	response.OK(ctx, dto.NewGetUserResponseWithTenant(user, depts, tenant))
}

// GetByID 查看指定用户信息（需组长及以上）
func (h *UserHandler) GetByID(ctx *gin.Context) {
	viewerID, _, ok := getViewerAuth(ctx)
	if !ok {
		return
	}
	idParam := ctx.Param("id")
	targetID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || targetID == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "用户ID无效")
		return
	}

	user, depts, err := h.userSvc.GetVisibleUser(ctx.Request.Context(), viewerID, uint(targetID))
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewGetUserResponse(user, depts))
}

// List 用户列表
func (h *UserHandler) List(ctx *gin.Context) {
	if _, _, ok := getViewerAuth(ctx); !ok {
		return
	}
	keyword := ctx.Query("keyword")
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))

	result, err := h.userSvc.SearchUsers(ctx.Request.Context(), keyword, page, pageSize)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewUserListResponse(result.Users, result.Page, result.PageSize, result.Total))
}

// ListVisible 按可见范围搜索用户（小组长及以上）
func (h *UserHandler) ListVisible(ctx *gin.Context) {
	viewerID, role, ok := getViewerAuth(ctx)
	if !ok {
		return
	}
	keyword := ctx.Query("keyword")
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))

	result, err := h.userSvc.SearchVisibleUsers(ctx.Request.Context(), viewerID, role, keyword, page, pageSize)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewUserListResponse(result.Users, result.Page, result.PageSize, result.Total))
}

// Refresh 刷新当前用户的钉钉信息
func (h *UserHandler) Refresh(ctx *gin.Context) {
	if _, _, ok := getViewerAuth(ctx); !ok {
		return
	}

	dingUserID := strings.TrimSpace(ctx.GetString(middleware.CtxKeyDingUserID))
	if dingUserID == "" {
		response.Fail(ctx, response.CodeUnauthorized, "未获取到钉钉用户ID")
		return
	}

	result, err := h.userSvc.Refresh(ctx.Request.Context(), dingUserID)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	resp := &dto.LoginUser{
		ID:         result.User.ID,
		DingUserID: result.User.DingUserID,
		Name:       result.User.Name,
		Avatar:     result.User.Avatar,
		Phone:      result.User.Phone,
		Role:       result.User.Role,
		RoleName:   consts.RoleName(result.User.Role),
		DeptIDs:    result.DeptIDs,
	}

	response.OKWithMessage(ctx, "个人信息同步成功", resp)
}

// SyncAll 批量刷新本地已有用户（管理员）
func (h *UserHandler) SyncAll(ctx *gin.Context) {
	var req dto.SyncAllUsersRequest
	if err := ctx.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.Fail(ctx, response.CodeInvalidParam, response.TranslateValidationError(err))
		return
	}

	result, err := h.userSvc.BatchRefresh(ctx.Request.Context(), req.Limit, req.Offset)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// UpdateStatus 更新用户考勤状态（管理员操作）
func (h *UserHandler) UpdateStatus(ctx *gin.Context) {
	idParam := ctx.Param("id")
	targetID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || targetID == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "用户ID无效")
		return
	}

	var req dto.UpdateUserStatusRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, response.TranslateValidationError(err))
		return
	}

	if err := h.userSvc.UpdateUserStatus(ctx.Request.Context(), uint(targetID), *req.Status); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OKWithMessage(ctx, "更新成功", nil)
}

// Delete 删除用户（软删除，管理员操作）
func (h *UserHandler) Delete(ctx *gin.Context) {
	idParam := ctx.Param("id")
	targetID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || targetID == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "用户ID无效")
		return
	}

	if err := h.userSvc.DeleteUser(ctx.Request.Context(), uint(targetID)); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OKWithMessage(ctx, "删除成功", nil)
}

// UpdateRole 更新用户角色（超级管理员操作）
func (h *UserHandler) UpdateRole(ctx *gin.Context) {
	idParam := ctx.Param("id")
	targetID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || targetID == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "用户ID无效")
		return
	}

	var req dto.UpdateUserRoleRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, response.TranslateValidationError(err))
		return
	}

	if err := h.userSvc.UpdateUserRole(ctx.Request.Context(), uint(targetID), *req.Role); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OKWithMessage(ctx, "更新成功", nil)
}
