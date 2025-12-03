package handler

import (
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

// Sync 从钉钉同步部门数据到数据库
func (h *DepartmentHandler) Sync(c *gin.Context) {
	ctx := c.Request.Context()

	if err := h.deptSrv.Sync(ctx); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OKWithMessage(c, "部门数据同步成功", nil)
}
