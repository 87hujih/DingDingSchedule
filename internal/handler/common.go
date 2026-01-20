package handler

import (
	"strconv"
	"strings"
	"time"

	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// AttendanceQueryParams 考勤查询通用参数
type AttendanceQueryParams struct {
	Date    time.Time
	DateStr string
	Week    int
	Section int
}

// ParseAttendanceQueryParams 解析考勤查询的公共参数 (date, week, section)
func ParseAttendanceQueryParams(ctx *gin.Context) (*AttendanceQueryParams, error) {
	// Date
	rawDate := strings.TrimSpace(ctx.Query("date"))
	if rawDate == "" {
		return nil, response.NewBizError(response.CodeMissingParam, "缺少 date 参数")
	}
	date, err := time.ParseInLocation("2006-01-02", rawDate, time.Local)
	if err != nil {
		return nil, response.NewBizError(response.CodeInvalidParam, "date 格式错误，应为 YYYY-MM-DD")
	}

	// Week
	rawWeek := strings.TrimSpace(ctx.Query("week"))
	if rawWeek == "" {
		return nil, response.NewBizError(response.CodeMissingParam, "缺少 week 参数")
	}
	week, err := strconv.Atoi(rawWeek)
	if err != nil || week <= 0 {
		return nil, response.NewBizError(response.CodeInvalidParam, "无效的 week")
	}

	// Section
	rawSection := strings.TrimSpace(ctx.Query("section"))
	if rawSection == "" {
		return nil, response.NewBizError(response.CodeMissingParam, "缺少 section 参数")
	}
	section, err := strconv.Atoi(rawSection)
	if err != nil || section <= 0 {
		return nil, response.NewBizError(response.CodeInvalidParam, "无效的 section")
	}

	return &AttendanceQueryParams{
		Date:    date,
		DateStr: rawDate,
		Week:    week,
		Section: section,
	}, nil
}

// ValidateWeekDate 校验周数与日期是否一致
func ValidateWeekDate(ctx *gin.Context, semesterSrv *service.SemesterService, date time.Time, week int) error {
	if semesterSrv == nil {
		return nil
	}
	if err := semesterSrv.ValidateWeekDate(ctx.Request.Context(), date, week); err != nil {
		return response.NewBizError(response.CodeInvalidParam, err.Error())
	}
	return nil
}

// ParseDeptIDsQuery 解析部门ID查询参数（逗号分隔）
// 返回 nil 表示不限制部门，返回空切片表示参数无效
func ParseDeptIDsQuery(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
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
			return nil, response.NewBizError(response.CodeInvalidParam, "dept_ids 参数包含无效值")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		deptIDs = append(deptIDs, id)
	}
	return deptIDs, nil
}
