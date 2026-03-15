package handler

import (
	"strconv"
	"strings"
	"time"

	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// AttendanceHandler 考勤处理器
type AttendanceHandler struct {
	attendanceSrv *service.AttendanceService
	semesterSrv   *service.SemesterService
}

func NewAttendanceHandler(attendanceSrv *service.AttendanceService, semesterSrv *service.SemesterService) *AttendanceHandler {
	return &AttendanceHandler{attendanceSrv: attendanceSrv, semesterSrv: semesterSrv}
}

// SlotAttendanceStatus 时段考勤状态（应到/请假，不依赖 courseID）
func (h *AttendanceHandler) SlotAttendanceStatus(ctx *gin.Context) {
	// 1. 获取查看者ID
	viewerID, err := h.getViewerID(ctx)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 2. 解析公共参数 (Date, Week, Section)
	params, err := ParseAttendanceQueryParams(ctx)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 3. 解析部门ID (特定参数)
	deptIDs, err := ParseDeptIDsQuery(ctx.Query("dept_ids"))
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 4. 可选校验: 星期一致性
	if err := h.validateDayOfWeek(ctx, params.Date); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 5. 校验周数与日期一致性
	if err := ValidateWeekDate(ctx, h.semesterSrv, params.Date, params.Week); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 6. 调用服务
	result, err := h.attendanceSrv.GetSlotAttendanceStatus(ctx.Request.Context(), viewerID, params.Date, params.Week, params.Section, deptIDs)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// WeekSlotsSummary 整周时段汇总（周视图专用，只返回数量）
func (h *AttendanceHandler) WeekSlotsSummary(ctx *gin.Context) {
	// 1. 鉴权
	viewerID, err := h.getViewerID(ctx)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 2. 解析 start_date（必须是周一）
	rawDate := strings.TrimSpace(ctx.Query("start_date"))
	if rawDate == "" {
		response.Fail(ctx, response.CodeMissingParam, "缺少 start_date 参数")
		return
	}
	startDate, parseErr := time.ParseInLocation("2006-01-02", rawDate, time.Local)
	if parseErr != nil {
		response.Fail(ctx, response.CodeInvalidParam, "start_date 格式错误，应为 YYYY-MM-DD")
		return
	}
	if startDate.Weekday() != time.Monday {
		response.Fail(ctx, response.CodeInvalidParam, "start_date 必须是周一")
		return
	}

	// 3. 解析 week
	rawWeek := strings.TrimSpace(ctx.Query("week"))
	if rawWeek == "" {
		response.Fail(ctx, response.CodeMissingParam, "缺少 week 参数")
		return
	}
	week, convErr := strconv.Atoi(rawWeek)
	if convErr != nil || week <= 0 {
		response.Fail(ctx, response.CodeInvalidParam, "无效的 week")
		return
	}

	// 4. 校验 week 与 start_date 一致性
	if err := ValidateWeekDate(ctx, h.semesterSrv, startDate, week); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 5. 解析可选 dept_ids
	deptIDs, err := ParseDeptIDsQuery(ctx.Query("dept_ids"))
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 6. 调用 service
	result, err := h.attendanceSrv.GetWeekSlotsAttendanceSummary(
		ctx.Request.Context(), viewerID, week, startDate, deptIDs,
	)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// SlotUserLeaveDetail 查看某用户在"时段"内的请假明细
func (h *AttendanceHandler) SlotUserLeaveDetail(ctx *gin.Context) {
	// 1. 获取查看者信息 (ID + Role)
	viewerID, viewerRole, err := h.getViewerInfo(ctx)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 2. 解析目标用户ID
	userID, err := h.parseUintParam(ctx, "user_id")
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 3. 解析公共参数 (Date, Week, Section)
	params, err := ParseAttendanceQueryParams(ctx)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 4. 调用服务
	result, err := h.attendanceSrv.GetSlotUserLeaveDetail(ctx.Request.Context(), viewerID, viewerRole, uint(userID), params.Week, params.Date, params.Section)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// getViewerID 仅获取用户ID
func (h *AttendanceHandler) getViewerID(ctx *gin.Context) (uint, error) {
	id := ctx.GetUint("user_id")
	if id == 0 {
		return 0, response.NewBizError(response.CodeUnauthorized, "未登录或ID无效")
	}
	return id, nil
}

// getViewerInfo 获取用户ID和角色
func (h *AttendanceHandler) getViewerInfo(ctx *gin.Context) (uint, int, error) {
	id, err := h.getViewerID(ctx)
	if err != nil {
		return 0, 0, err
	}
	roleVal, exists := ctx.Get("user_role")
	if !exists {
		return 0, 0, response.NewBizError(response.CodeUnauthorized, "用户角色无效")
	}
	role, ok := roleVal.(int)
	if !ok {
		return 0, 0, response.NewBizError(response.CodeUnauthorized, "用户角色格式错误")
	}
	return id, role, nil
}

// parseUintParam 解析 Path 参数中的 ID
func (h *AttendanceHandler) parseUintParam(ctx *gin.Context, key string) (uint64, error) {
	valStr := ctx.Param(key)
	val, err := strconv.ParseUint(valStr, 10, 64)
	if err != nil || val == 0 {
		return 0, response.NewBizError(response.CodeInvalidParam, "无效的 "+key)
	}
	return val, nil
}

// validateDayOfWeek 校验前端传来的星期几是否正确 (可选逻辑)
func (h *AttendanceHandler) validateDayOfWeek(ctx *gin.Context, date time.Time) error {
	rawDayOfWeek := strings.TrimSpace(ctx.Query("day_of_week"))
	if rawDayOfWeek == "" {
		return nil
	}
	dayOfWeek, err := strconv.Atoi(rawDayOfWeek)
	if err != nil || dayOfWeek <= 0 {
		return response.NewBizError(response.CodeInvalidParam, "无效的 day_of_week")
	}
	derived := int(date.Weekday())
	if derived == 0 {
		derived = 7
	}
	if dayOfWeek != derived {
		return response.NewBizError(response.CodeInvalidParam, "day_of_week 与 date 不一致")
	}
	return nil
}
