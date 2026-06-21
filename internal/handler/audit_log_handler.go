package handler

import (
	"strconv"
	"time"

	"schedule_server/internal/dto"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// AuditLogHandler 审计日志处理器
type AuditLogHandler struct {
	auditLogSrv *service.AuditLogService
}

// NewAuditLogHandler 创建审计日志处理器实例
func NewAuditLogHandler(auditLogSrv *service.AuditLogService) *AuditLogHandler {
	return &AuditLogHandler{auditLogSrv: auditLogSrv}
}

// List 分页查询审计日志
// GET /api/admin/audit-logs?user_id=1&method=POST&path=/api/users&start_date=2026-01-01&end_date=2026-02-01&page=1&page_size=20
func (h *AuditLogHandler) List(ctx *gin.Context) {
	filter := repository.AuditLogFilter{}

	if v := ctx.Query("user_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil || id == 0 {
			response.FailWithError(ctx, response.ErrInvalidParamWithMsg("user_id 格式错误"))
			return
		}
		filter.UserID = uint(id)
	}

	filter.Method = ctx.Query("method")
	filter.Path = ctx.Query("path")

	if v := ctx.Query("start_date"); v != "" {
		t, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			response.FailWithError(ctx, response.ErrInvalidParamWithMsg("start_date 格式错误，应为 YYYY-MM-DD"))
			return
		}
		filter.StartDate = t
	}

	if v := ctx.Query("end_date"); v != "" {
		t, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			response.FailWithError(ctx, response.ErrInvalidParamWithMsg("end_date 格式错误，应为 YYYY-MM-DD"))
			return
		}
		// end_date 取次日零点，实现 [start, end) 区间
		filter.EndDate = t.AddDate(0, 0, 1)
	}

	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	filter.Page = page
	filter.PageSize = pageSize

	logs, total, err := h.auditLogSrv.List(ctx.Request.Context(), filter)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	items := make([]*dto.AuditLogItem, 0, len(logs))
	for _, l := range logs {
		items = append(items, dto.NewAuditLogItem(l))
	}

	response.OK(ctx, &dto.AuditLogListResponse{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
		Items:    items,
	})
}
