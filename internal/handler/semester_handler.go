package handler

import (
	"strconv"
	"time"

	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// SemesterHandler 学期处理器
type SemesterHandler struct {
	semesterSrv *service.SemesterService
}

// NewSemesterHandler 创建学期处理器
func NewSemesterHandler(semesterSrv *service.SemesterService) *SemesterHandler {
	return &SemesterHandler{semesterSrv: semesterSrv}
}

// List 查询所有学期
// GET /semesters
func (h *SemesterHandler) List(ctx *gin.Context) {
	semesters, err := h.semesterSrv.List(ctx.Request.Context())
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewSemesterListResponse(semesters))
}

// Create 创建学期
// POST /semesters
func (h *SemesterHandler) Create(ctx *gin.Context) {
	var req dto.CreateSemesterRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, err.Error())
		return
	}

	// 解析日期
	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		response.Fail(ctx, response.CodeInvalidParam, "start_date 格式错误，应为 YYYY-MM-DD")
		return
	}

	semester, err := h.semesterSrv.Create(ctx.Request.Context(), req.Name, startDate, req.TotalWeek)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.CreateSemesterResponse{ID: semester.ID})
}

// Update 更新学期
// PUT /semesters/:id
func (h *SemesterHandler) Update(ctx *gin.Context) {
	semesterID, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.Fail(ctx, response.CodeInvalidParam, "无效的学期ID")
		return
	}

	var req dto.UpdateSemesterRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, err.Error())
		return
	}

	// 解析日期（可选）
	var startDate *time.Time
	if req.StartDate != "" {
		t, err := time.Parse("2006-01-02", req.StartDate)
		if err != nil {
			response.Fail(ctx, response.CodeInvalidParam, "start_date 格式错误，应为 YYYY-MM-DD")
			return
		}
		startDate = &t
	}

	if err := h.semesterSrv.Update(ctx.Request.Context(), uint(semesterID), req.Name, startDate, req.TotalWeek); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, nil)
}

// Delete 删除学期
// DELETE /semesters/:id
func (h *SemesterHandler) Delete(ctx *gin.Context) {
	semesterID, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.Fail(ctx, response.CodeInvalidParam, "无效的学期ID")
		return
	}

	if err := h.semesterSrv.Delete(ctx.Request.Context(), uint(semesterID)); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, nil)
}
