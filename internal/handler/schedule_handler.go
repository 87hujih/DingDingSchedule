package handler

import (
	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"
	"strconv"

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

	count, err := h.scheduleSrv.ImportFromFile(ctx.Request.Context(), userID, tmpPath)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.ImportScheduleResponse{Inserted: count})
}

// List 列出课表（按周查询，不分页）
// GET /schedules/week?week=3&user_id=2
// user_id 可选；user_id 需具备相应权限才可查看他人
func (h *ScheduleHandler) List(ctx *gin.Context) {
	userID := ctx.GetUint("user_id")
	roleVal, exists := ctx.Get("user_role")
	if userID == 0 || !exists {
		response.Fail(ctx, response.CodeUnauthorized, "未登录或ID无效")
		return
	}
	userRole, ok := roleVal.(int)
	if !ok {
		response.Fail(ctx, response.CodeUnauthorized, "用户角色无效")
		return
	}

	rawWeek := ctx.Query("week")
	if rawWeek == "" {
		response.Fail(ctx, response.CodeMissingParam, "缺少 week 参数")
		return
	}
	week, err := strconv.Atoi(rawWeek)
	if err != nil || week <= 0 {
		response.Fail(ctx, response.CodeInvalidParam, "无效的 week")
		return
	}
	targetUserID := userID
	if rawUserID := ctx.Query("user_id"); rawUserID != "" {
		if parsed, err := strconv.ParseUint(rawUserID, 10, 64); err != nil {
			response.Fail(ctx, response.CodeInvalidParam, "无效的用户ID")
			return
		} else if parsed != 0 {
			targetUserID = uint(parsed)
		}
	}

	result, err := h.scheduleSrv.ListByWeek(ctx.Request.Context(), userID, userRole, targetUserID, week)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewScheduleListResponse(&dto.ScheduleListParams{
		Week:    week,
		Courses: result.Courses,
	}))
}

// ListAll 获取全部课程（不按周过滤）
// GET /schedules/all?page=1&page_size=10
// 仅查看自己的课表，不做角色校验
func (h *ScheduleHandler) ListAll(ctx *gin.Context) {
	userID := ctx.GetUint("user_id")
	if userID == 0 {
		response.Fail(ctx, response.CodeUnauthorized, "未登录或ID无效")
		return
	}

	page, _ := strconv.Atoi(ctx.Query("page"))
	pageSize, _ := strconv.Atoi(ctx.Query("page_size"))

	result, err := h.scheduleSrv.ListAll(ctx.Request.Context(), userID, page, pageSize)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.NewAllCoursesListResponse(&dto.AllCoursesListParams{
		Page:     result.Page,
		PageSize: result.PageSize,
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
