package handler

import (
	"strings"
	"time"

	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// AttendanceRecordHandler 考勤记录处理器（打卡统计）
type AttendanceRecordHandler struct {
	attendanceRecordSrv *service.AttendanceRecordService
	semesterSrv         *service.SemesterService
}

// NewAttendanceRecordHandler 创建考勤记录处理器实例
func NewAttendanceRecordHandler(
	attendanceRecordSrv *service.AttendanceRecordService,
	semesterSrv *service.SemesterService,
) *AttendanceRecordHandler {
	return &AttendanceRecordHandler{
		attendanceRecordSrv: attendanceRecordSrv,
		semesterSrv:         semesterSrv,
	}
}

// GetAttendanceDetail 获取考勤详情
// GET /api/admin/attendance/record/detail?date=2026-01-18&week=1&section=1&dept_ids=1,2,3
func (h *AttendanceRecordHandler) GetAttendanceDetail(ctx *gin.Context) {
	// 1. 解析公共参数 (Date, Week, Section)
	params, err := ParseAttendanceQueryParams(ctx)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 2. 校验周数与日期一致性
	if err := ValidateWeekDate(ctx, h.semesterSrv, params.Date, params.Week); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 3. 解析部门ID过滤参数
	deptIDs, err := ParseDeptIDsQuery(ctx.Query("dept_ids"))
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 4. 构建请求
	req := &dto.AttendanceDetailRequest{
		Date:    params.DateStr,
		Week:    params.Week,
		Section: params.Section,
		DeptIDs: deptIDs,
	}

	// 5. 调用服务获取考勤详情
	result, err := h.attendanceRecordSrv.GetAttendanceDetail(ctx.Request.Context(), req)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// TriggerAttendanceStatistics 手动触发考勤统计
// POST /api/admin/attendance/record/trigger
func (h *AttendanceRecordHandler) TriggerAttendanceStatistics(ctx *gin.Context) {
	// 1. 解析请求体
	var req dto.AttendanceTriggerRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.FailWithError(ctx, response.ErrInvalidParamWithMsg(err.Error()))
		return
	}

	// 2. 解析日期并校验
	date, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
	if err != nil {
		response.FailWithError(ctx, response.NewBizError(response.CodeInvalidParam, "date 格式错误，应为 YYYY-MM-DD"))
		return
	}

	// 3. 校验周数与日期一致性
	if err := ValidateWeekDate(ctx, h.semesterSrv, date, req.Week); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 4. 转换为查询请求
	detailReq := &dto.AttendanceDetailRequest{
		Date:    req.Date,
		Week:    req.Week,
		Section: req.Section,
		DeptIDs: req.DeptIDs,
	}

	// 5. 获取考勤详情
	result, err := h.attendanceRecordSrv.GetAttendanceDetail(ctx.Request.Context(), detailReq)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 6. 保存到数据库
	if err := h.attendanceRecordSrv.SaveAttendanceRecord(ctx.Request.Context(), result); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// GetAttendanceSnapshot 获取考勤统计快照（从数据库读取已保存的记录）
// GET /api/admin/attendance/record/snapshot?date=2026-01-18&week=1&section=1&dept_ids=1,2,3
func (h *AttendanceRecordHandler) GetAttendanceSnapshot(ctx *gin.Context) {
	// 1. 解析公共参数 (Date, Week, Section)
	params, err := ParseAttendanceQueryParams(ctx)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 2. 校验周数与日期一致性
	if err := ValidateWeekDate(ctx, h.semesterSrv, params.Date, params.Week); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 3. 解析部门ID过滤参数
	deptIDs, err := ParseDeptIDsQuery(ctx.Query("dept_ids"))
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 4. 构建请求
	req := &dto.AttendanceDetailRequest{
		Date:    params.DateStr,
		Week:    params.Week,
		Section: params.Section,
		DeptIDs: deptIDs,
	}

	// 5. 从数据库获取已保存的记录
	result, err := h.attendanceRecordSrv.GetAttendanceRecordFromDB(ctx.Request.Context(), req)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}

// GetAttendanceRecords 获取某天的所有考勤记录
// GET /api/admin/attendance/record/list?date=2026-01-18&dept_ids=1,2,3
func (h *AttendanceRecordHandler) GetAttendanceRecords(ctx *gin.Context) {
	rawDate := strings.TrimSpace(ctx.Query("date"))
	if rawDate == "" {
		response.FailWithError(ctx, response.NewBizError(response.CodeMissingParam, "缺少 date 参数"))
		return
	}

	date, err := time.ParseInLocation("2006-01-02", rawDate, time.Local)
	if err != nil {
		response.FailWithError(ctx, response.NewBizError(response.CodeInvalidParam, "date 格式错误，应为 YYYY-MM-DD"))
		return
	}

	// 解析部门ID过滤参数
	deptIDs, err := ParseDeptIDsQuery(ctx.Query("dept_ids"))
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	// 调用服务获取考勤记录
	result, err := h.attendanceRecordSrv.GetAttendanceRecordsByDate(ctx.Request.Context(), date, deptIDs)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, result)
}
