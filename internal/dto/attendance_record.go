package dto

import (
	"encoding/json"
	"schedule_server/internal/model"
	"time"
)

// ========== 请求 ==========

// AttendanceDetailRequest 考勤详情查询请求
type AttendanceDetailRequest struct {
	Date    string  `form:"date" binding:"required"`          // YYYY-MM-DD
	Week    int     `form:"week" binding:"required,min=1"`    // 周次
	Section int     `form:"section" binding:"required,min=1"` // 节次
	DeptIDs []int64 `form:"-"`                                // 部门ID列表（可选过滤）
}

// WeeklyAttendanceRankingRequest 周考勤排行请求
type WeeklyAttendanceRankingRequest struct {
	WeekOffset int     `form:"week_offset"` // 周偏移量：0=本周，1=上周，2=上上周
	DeptIDs    []int64 `form:"-"`           // 部门ID列表（可选过滤）
}

// AttendanceTriggerRequest 手动触发考勤统计请求
type AttendanceTriggerRequest struct {
	Date    string  `json:"date" binding:"required"`          // YYYY-MM-DD
	Week    int     `json:"week" binding:"required,min=1"`    // 周次
	Section int     `json:"section" binding:"required,min=1"` // 节次
	DeptIDs []int64 `json:"dept_ids"`                         // 部门ID列表（可选过滤）
}

// AttendanceTextRequest 考勤文本查询请求（用于复制到群里）
type AttendanceTextRequest struct {
	Date    string  `form:"date" binding:"required"`          // YYYY-MM-DD
	Week    int     `form:"week" binding:"required,min=1"`    // 周次
	Section int     `form:"section" binding:"required,min=1"` // 节次
	DeptIDs []int64 `form:"-"`                                // 部门ID列表（可选过滤）
}

// ========== 响应 ==========

// AttendanceDetailResponse 考勤详情响应
type AttendanceDetailResponse struct {
	RecordID   uint                 `json:"record_id"`
	Date       string               `json:"date"`
	Week       int                  `json:"week"`
	Section    int                  `json:"section"`
	SlotTime   SlotTimeInfo         `json:"slot_time"`
	Statistics AttendanceStatistics `json:"statistics"`
	Users      AttendanceUserLists  `json:"users"`
}

// SlotTimeInfo 课节时间信息
type SlotTimeInfo struct {
	Start string `json:"start"` // HH:MM
	End   string `json:"end"`   // HH:MM
}

// AttendanceStatistics 考勤统计数据
type AttendanceStatistics struct {
	ShouldAttend int `json:"should_attend"` // 应到人数
	OnTime       int `json:"on_time"`       // 正常打卡人数
	Leave        int `json:"leave"`         // 请假人数
	NotArrived   int `json:"not_arrived"`   // 未到人数（含迟到和缺勤）
	RestDay      int `json:"rest_day"`      // 休息人数
	HasCourse    int `json:"has_course"`    // 有课人数
}

// AttendanceUserLists 考勤人员列表
type AttendanceUserLists struct {
	ShouldAttend []AttendanceUserBasic `json:"should_attend"`
	OnTime       []AttendanceUserCheck `json:"on_time"`
	Leave        []AttendanceUserLeave `json:"leave"`
	NotArrived   []AttendanceUserBasic `json:"not_arrived"` // 未到（含迟到和缺勤）
	RestDay      []AttendanceUserBasic `json:"rest_day"`    // 休息日
	HasCourse    []AttendanceUserBasic `json:"has_course"`  // 有课
}

// AttendanceUserBasic 基础用户信息
type AttendanceUserBasic struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Avatar   string `json:"avatar"`
	DeptName string `json:"dept_name"`
}

// AttendanceUserCheck 打卡用户信息
type AttendanceUserCheck struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Avatar    string    `json:"avatar"`
	DeptName  string    `json:"dept_name"`
	CheckTime time.Time `json:"check_time"`
}

// AttendanceUserLeave 请假用户信息
type AttendanceUserLeave struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar"`
	DeptName  string `json:"dept_name"`
	LeaveType string `json:"leave_type"`
	Reason    string `json:"reason"`
}

// AttendanceTextResponse 考勤文本响应（用于复制到群里）
type AttendanceTextResponse struct {
	Title      string   `json:"title"`      // 标题（如：2026-02-05 第3周 第1节 考勤）
	Statistics string   `json:"statistics"` // 统计信息（如：应到30人，正常打卡25人，请假3人，未到2人）
	Content    []string `json:"content"`    // 分类人员列表（每行一个分类）
	FullText   string   `json:"full_text"`  // 完整文本（可直接复制）
}

// AttendanceRankingItem 考勤排行项
type AttendanceRankingItem struct {
	UserID    uint   `json:"user_id"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar"`
	LateCount int    `json:"late_count"`
}

// NewAttendanceDetailResponseFromRecord 从数据库记录构造响应
func NewAttendanceDetailResponseFromRecord(
	record *model.AttendanceRecord,
	slotStart, slotEnd string,
	userMap map[uint]*model.User,
	userDeptNames map[uint]string,
) *AttendanceDetailResponse {
	// Helper to safe get user info
	getUserInfo := func(id uint) (name, avatar, dept string) {
		name = "Unknown"
		if u, ok := userMap[id]; ok {
			name = u.Name
			avatar = u.Avatar
		}
		if d, ok := userDeptNames[id]; ok {
			dept = d
		}
		return name, avatar, dept
	}

	// Parse OnTime
	var onTimeUsers []StoredUserCheck
	if record.OnTimeIDs != "" && record.OnTimeIDs != "[]" {
		_ = json.Unmarshal([]byte(record.OnTimeIDs), &onTimeUsers)
	}
	onTimeList := make([]AttendanceUserCheck, 0, len(onTimeUsers))
	for _, u := range onTimeUsers {
		if _, ok := userMap[u.ID]; !ok {
			continue
		}
		name, avatar, dept := getUserInfo(u.ID)
		onTimeList = append(onTimeList, AttendanceUserCheck{
			ID:        u.ID,
			Name:      name,
			Avatar:    avatar,
			DeptName:  dept,
			CheckTime: time.Unix(u.CheckTime, 0),
		})
	}

	// Parse Leave
	var leaveUsers []StoredUserLeave
	if record.LeaveIDs != "" && record.LeaveIDs != "[]" {
		_ = json.Unmarshal([]byte(record.LeaveIDs), &leaveUsers)
	}
	leaveList := make([]AttendanceUserLeave, 0, len(leaveUsers))
	for _, u := range leaveUsers {
		if _, ok := userMap[u.ID]; !ok {
			continue
		}
		name, avatar, dept := getUserInfo(u.ID)
		leaveList = append(leaveList, AttendanceUserLeave{
			ID:        u.ID,
			Name:      name,
			Avatar:    avatar,
			DeptName:  dept,
			LeaveType: u.LeaveType,
			Reason:    u.Reason,
		})
	}

	// Parse NotArrived
	var notArrivedIDs []uint
	if record.NotArrivedIDs != "" && record.NotArrivedIDs != "[]" {
		_ = json.Unmarshal([]byte(record.NotArrivedIDs), &notArrivedIDs)
	}
	notArrivedList := make([]AttendanceUserBasic, 0, len(notArrivedIDs))
	for _, id := range notArrivedIDs {
		if _, ok := userMap[id]; !ok {
			continue
		}
		name, avatar, dept := getUserInfo(id)
		notArrivedList = append(notArrivedList, AttendanceUserBasic{
			ID:       id,
			Name:     name,
			Avatar:   avatar,
			DeptName: dept,
		})
	}

	// Parse RestDay
	var restDayIDs []uint
	if record.RestDayIDs != "" && record.RestDayIDs != "[]" {
		_ = json.Unmarshal([]byte(record.RestDayIDs), &restDayIDs)
	}
	restDayList := make([]AttendanceUserBasic, 0, len(restDayIDs))
	for _, id := range restDayIDs {
		if _, ok := userMap[id]; !ok {
			continue
		}
		name, avatar, dept := getUserInfo(id)
		restDayList = append(restDayList, AttendanceUserBasic{
			ID:       id,
			Name:     name,
			Avatar:   avatar,
			DeptName: dept,
		})
	}

	// Parse HasCourse
	var hasCourseIDs []uint
	if record.HasCourseIDs != "" && record.HasCourseIDs != "[]" {
		_ = json.Unmarshal([]byte(record.HasCourseIDs), &hasCourseIDs)
	}
	hasCourseList := make([]AttendanceUserBasic, 0, len(hasCourseIDs))
	for _, id := range hasCourseIDs {
		if _, ok := userMap[id]; !ok {
			continue
		}
		name, avatar, dept := getUserInfo(id)
		hasCourseList = append(hasCourseList, AttendanceUserBasic{
			ID:       id,
			Name:     name,
			Avatar:   avatar,
			DeptName: dept,
		})
	}

	// ShouldAttend = OnTime + Leave + NotArrived
	shouldAttendList := make([]AttendanceUserBasic, 0)
	seen := make(map[uint]bool)

	add := func(id uint) {
		if seen[id] {
			return
		}
		if _, ok := userMap[id]; !ok {
			return
		}
		name, avatar, dept := getUserInfo(id)
		shouldAttendList = append(shouldAttendList, AttendanceUserBasic{
			ID:       id,
			Name:     name,
			Avatar:   avatar,
			DeptName: dept,
		})
		seen[id] = true
	}

	for _, u := range onTimeUsers {
		add(u.ID)
	}
	for _, u := range leaveUsers {
		add(u.ID)
	}
	for _, id := range notArrivedIDs {
		add(id)
	}

	return &AttendanceDetailResponse{
		RecordID: record.ID,
		Date:     record.Date.Format("2006-01-02"),
		Week:     record.Week,
		Section:  record.Section,
		SlotTime: SlotTimeInfo{
			Start: slotStart,
			End:   slotEnd,
		},
		Statistics: AttendanceStatistics{
			ShouldAttend: len(shouldAttendList),
			OnTime:       len(onTimeList),
			Leave:        len(leaveList),
			NotArrived:   len(notArrivedList),
			RestDay:      len(restDayList),
			HasCourse:    len(hasCourseList),
		},
		Users: AttendanceUserLists{
			ShouldAttend: shouldAttendList,
			OnTime:       onTimeList,
			Leave:        leaveList,
			NotArrived:   notArrivedList,
			RestDay:      restDayList,
			HasCourse:    hasCourseList,
		},
	}
}

// WeeklyAttendanceRankingResponse 周考勤排行响应
type WeeklyAttendanceRankingResponse struct {
	Items []AttendanceRankingItem `json:"items"`
}

// ========== 存储结构（用于JSON序列化到数据库） ==========

// StoredUserCheck 存储的打卡用户信息
type StoredUserCheck struct {
	ID        uint  `json:"id"`
	CheckTime int64 `json:"check_time"` // Unix时间戳
}

// StoredUserLeave 存储的请假用户信息
type StoredUserLeave struct {
	ID        uint   `json:"id"`
	LeaveType string `json:"leave_type"`
	Reason    string `json:"reason"`
}

// ========== 构造函数 ==========

// NewAttendanceDetailResponse 从统计数据构造响应
func NewAttendanceDetailResponse(
	date string,
	week, section int,
	slotStart, slotEnd string,
	shouldAttend []model.User,
	onTime []AttendanceUserCheck,
	leave []AttendanceUserLeave,
	notArrived []AttendanceUserBasic,
	restDay []AttendanceUserBasic,
	hasCourse []AttendanceUserBasic,
) *AttendanceDetailResponse {
	// 构建应到人员列表
	shouldAttendList := make([]AttendanceUserBasic, 0, len(shouldAttend))
	for _, u := range shouldAttend {
		shouldAttendList = append(shouldAttendList, AttendanceUserBasic{
			ID:   u.ID,
			Name: u.Name,
		})
	}

	if restDay == nil {
		restDay = []AttendanceUserBasic{}
	}
	if hasCourse == nil {
		hasCourse = []AttendanceUserBasic{}
	}

	// 其他列表由调用方填充
	return &AttendanceDetailResponse{
		Date:    date,
		Week:    week,
		Section: section,
		SlotTime: SlotTimeInfo{
			Start: slotStart,
			End:   slotEnd,
		},
		Statistics: AttendanceStatistics{
			ShouldAttend: len(shouldAttend),
			OnTime:       len(onTime),
			Leave:        len(leave),
			NotArrived:   len(notArrived),
			RestDay:      len(restDay),
			HasCourse:    len(hasCourse),
		},
		Users: AttendanceUserLists{
			ShouldAttend: shouldAttendList,
			OnTime:       onTime,
			Leave:        leave,
			NotArrived:   notArrived,
			RestDay:      restDay,
			HasCourse:    hasCourse,
		},
	}
}
