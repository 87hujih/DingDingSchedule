package handler

import (
	"errors"
	"strconv"
	"strings"

	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// AttendanceHandler 考勤处理器
type AttendanceHandler struct {
	attendanceSrv *service.AttendanceService
}

func NewAttendanceHandler(attendanceSrv *service.AttendanceService) *AttendanceHandler {
	return &AttendanceHandler{attendanceSrv: attendanceSrv}
}

// CourseAttendanceStatus 课节考勤状态（应到/请假）
// GET /attendance/courses/:id/status?week=3&dept_ids=1,2,3
//
// 说明：
// - 所有登录用户可访问（不做角色判定）
// - dept_ids 可选：
//   - 不传/为空：按“全体参与考勤用户（status=1）”计算
//   - 传多个：按“这些部门的并集用户（status=1）”计算
func (h *AttendanceHandler) CourseAttendanceStatus(ctx *gin.Context) {
	viewerID := ctx.GetUint("user_id")
	if viewerID == 0 {
		response.Fail(ctx, response.CodeUnauthorized, "未登录或ID无效")
		return
	}

	courseID, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || courseID == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "无效的课程ID")
		return
	}

	week, _ := strconv.Atoi(ctx.Query("week"))

	deptIDs, err := parseDeptIDsQuery(ctx.Query("dept_ids"))
	if err != nil {
		response.Fail(ctx, response.CodeInvalidParam, err.Error())
		return
	}

	result, err := h.attendanceSrv.GetCourseAttendanceStatus(ctx.Request.Context(), viewerID, uint(courseID), week, deptIDs)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// CourseUserLeaveDetail 查看某用户在该课程课节时间窗口内的请假明细（点击人员后查看）
// GET /attendance/courses/:course_id/users/:user_id/leave?week=3
func (h *AttendanceHandler) CourseUserLeaveDetail(ctx *gin.Context) {
	viewerID := ctx.GetUint("user_id")
	roleVal, exists := ctx.Get("user_role")
	if viewerID == 0 || !exists {
		response.Fail(ctx, response.CodeUnauthorized, "未登录或ID无效")
		return
	}
	viewerRole, ok := roleVal.(int)
	if !ok {
		response.Fail(ctx, response.CodeUnauthorized, "用户角色无效")
		return
	}

	courseID, err := strconv.ParseUint(ctx.Param("course_id"), 10, 64)
	if err != nil || courseID == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "无效的课程ID")
		return
	}
	userID, err := strconv.ParseUint(ctx.Param("user_id"), 10, 64)
	if err != nil || userID == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "无效的用户ID")
		return
	}

	week, _ := strconv.Atoi(ctx.Query("week"))

	result, err := h.attendanceSrv.GetCourseUserLeaveDetail(ctx.Request.Context(), viewerID, viewerRole, uint(courseID), uint(userID), week)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// parseDeptIDsQuery 解析 dept_ids 查询参数（逗号分隔多个部门ID）。
// - 返回 nil,nil 表示“不按部门过滤”（查看全部）
// - 会去重、忽略空项
func parseDeptIDsQuery(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// 不传则表示不按部门筛选（查看全部）
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	deptIDs := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("dept_ids 参数无效")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		deptIDs = append(deptIDs, id)
	}
	if len(deptIDs) == 0 {
		return nil, errors.New("dept_ids 参数无效")
	}
	return deptIDs, nil
}
