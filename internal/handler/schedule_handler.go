package handler

import (
	"strconv"

	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// ScheduleHandler 课表处理器
type ScheduleHandler struct {
	scheduleSrv *service.ScheduleService
}

// NewScheduleHandler 创建课表处理器
func NewScheduleHandler(scheduleSrv *service.ScheduleService) *ScheduleHandler {
	return &ScheduleHandler{scheduleSrv: scheduleSrv}
}

// Import 导入课表文件
func (h *ScheduleHandler) Import(ctx *gin.Context) {
	userID := ctx.GetUint("user_id")

	semester := ctx.PostForm("semester")
	fileHeader, err := ctx.FormFile("file")
	if err != nil {
		response.Fail(ctx, response.CodeMissingParam, "缺少文件参数")
		return
	}

	tmpPath, cleanup, err := h.scheduleSrv.SaveUploadToTemp(ctx.Request.Context(), fileHeader.Filename, fileHeader.Open)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	count, err := h.scheduleSrv.ImportFromFile(ctx.Request.Context(), userID, semester, tmpPath)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.ImportScheduleResponse{Inserted: count})
}

// List 列出课表（按周查询，不分页）
// GET /schedules?semester=2025-Spring&week=3
// week 可选，不传则返回当前周的课程
func (h *ScheduleHandler) List(ctx *gin.Context) {
	userID := ctx.GetUint("user_id")
	semester := ctx.Query("semester")
	week, _ := strconv.Atoi(ctx.Query("week"))

	result, err := h.scheduleSrv.ListByWeek(ctx.Request.Context(), userID, semester, week)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewScheduleListResponse(&dto.ScheduleListParams{
		CurrentWeek: result.CurrentWeek,
		TotalWeek:   result.TotalWeek,
		Week:        week,
		Courses:     result.Courses,
	}))
}

// ListAll 获取全部课程（不按周过滤，用于管理）
// GET /schedules/all?semester=2025-Spring&page=1&page_size=10
func (h *ScheduleHandler) ListAll(ctx *gin.Context) {
	userID := ctx.GetUint("user_id")
	semester := ctx.Query("semester")

	page, _ := strconv.Atoi(ctx.Query("page"))
	pageSize, _ := strconv.Atoi(ctx.Query("page_size"))

	result, err := h.scheduleSrv.ListAll(ctx.Request.Context(), userID, semester, page, pageSize)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewAllCoursesListResponse(&dto.AllCoursesListParams{
		Page:     page,
		PageSize: pageSize,
		Total:    result.Total,
		Courses:  result.Courses,
	}))
}

// Create 手动添加课程
// POST /schedules
func (h *ScheduleHandler) Create(ctx *gin.Context) {
	userID := ctx.GetUint("user_id")

	var req dto.CreateCourseRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, err.Error())
		return
	}

	courseID, err := h.scheduleSrv.CreateCourse(ctx.Request.Context(), userID, &req)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.CreateCourseResponse{ID: courseID})
}

// Update 更新课程
// PUT /schedules/:id
func (h *ScheduleHandler) Update(ctx *gin.Context) {
	userID := ctx.GetUint("user_id")

	courseID, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.Fail(ctx, response.CodeInvalidParam, "无效的课程ID")
		return
	}

	var req dto.UpdateCourseRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, err.Error())
		return
	}

	if err := h.scheduleSrv.UpdateCourse(ctx.Request.Context(), userID, uint(courseID), &req); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, nil)
}

// Delete 删除课程
// DELETE /schedules/:id
func (h *ScheduleHandler) Delete(ctx *gin.Context) {
	userID := ctx.GetUint("user_id")

	courseID, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.Fail(ctx, response.CodeInvalidParam, "无效的课程ID")
		return
	}

	if err := h.scheduleSrv.DeleteCourse(ctx.Request.Context(), userID, uint(courseID)); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, nil)
}
