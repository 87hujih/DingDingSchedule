package handler

import (
	"strconv"

	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// DepartmentHandler 部门处理器
type DepartmentHandler struct {
	deptSrv *service.DepartmentService
}

// NewDepartmentHandler 创建部门处理器
func NewDepartmentHandler(deptSrv *service.DepartmentService) *DepartmentHandler {
	return &DepartmentHandler{deptSrv: deptSrv}
}

// List 获取所有叶子部门（需要考勤的部门）
func (h *DepartmentHandler) List(c *gin.Context) {
	depts, err := h.deptSrv.List(c.Request.Context())
	if err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OK(c, dto.NewDepartmentListResponse(depts))
}

// Sync 从钉钉同步部门数据到数据库
func (h *DepartmentHandler) Sync(c *gin.Context) {
	ctx := c.Request.Context()

	if err := h.deptSrv.Sync(ctx); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OKWithMessage(c, "部门数据同步成功", nil)
}

// UpdateStatus 更新部门考勤状态（管理员操作）
func (h *DepartmentHandler) UpdateStatus(c *gin.Context) {
	idParam := c.Param("id")
	deptID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || deptID <= 0 {
		response.Fail(c, response.CodeInvalidParam, "部门ID无效")
		return
	}

	var req dto.UpdateDeptStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeInvalidParam, response.TranslateValidationError(err))
		return
	}

	if err := h.deptSrv.UpdateStatus(c.Request.Context(), deptID, req.Status); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OKWithMessage(c, "更新成功", nil)
}

// Delete 删除部门（软删除，管理员操作）
func (h *DepartmentHandler) Delete(c *gin.Context) {
	idParam := c.Param("id")
	deptID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || deptID <= 0 {
		response.Fail(c, response.CodeInvalidParam, "部门ID无效")
		return
	}

	if err := h.deptSrv.Delete(c.Request.Context(), deptID); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OKWithMessage(c, "删除成功", nil)
}
