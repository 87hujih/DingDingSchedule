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

// AttendanceTriggerRequest 手动触发考勤统计请求
type AttendanceTriggerRequest struct {
	Date    string  `json:"date" binding:"required"`          // YYYY-MM-DD
	Week    int     `json:"week" binding:"required,min=1"`    // 周次
	Section int     `json:"section" binding:"required,min=1"` // 节次
	DeptIDs []int64 `json:"dept_ids"`                         // 部门ID列表（可选过滤）
}

// ========== 响应 ==========

// AttendanceDetailResponse 考勤详情响应
type AttendanceDetailResponse struct {
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
}

// AttendanceUserLists 考勤人员列表
type AttendanceUserLists struct {
	ShouldAttend []AttendanceUserBasic `json:"should_attend"`
	OnTime       []AttendanceUserCheck `json:"on_time"`
	Leave        []AttendanceUserLeave `json:"leave"`
	NotArrived   []AttendanceUserBasic `json:"not_arrived"` // 未到（含迟到和缺勤）
}

// AttendanceUserBasic 基础用户信息
type AttendanceUserBasic struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// AttendanceUserCheck 打卡用户信息
type AttendanceUserCheck struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	CheckTime time.Time `json:"check_time"`
}

// AttendanceUserLeave 请假用户信息
type AttendanceUserLeave struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	LeaveType string `json:"leave_type"`
	Reason    string `json:"reason"`
}

// AttendanceRankingItem 考勤排行项
type AttendanceRankingItem struct {
	UserID    uint   `json:"user_id"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar"`
	LateCount int    `json:"late_count"`
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
) *AttendanceDetailResponse {
	// 构建应到人员列表
	shouldAttendList := make([]AttendanceUserBasic, 0, len(shouldAttend))
	for _, u := range shouldAttend {
		shouldAttendList = append(shouldAttendList, AttendanceUserBasic{
			ID:   u.ID,
			Name: u.Name,
		})
	}

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
		},
		Users: AttendanceUserLists{
			ShouldAttend: shouldAttendList,
			OnTime:       onTime,
			Leave:        leave,
			NotArrived:   notArrived,
		},
	}
}

// NewAttendanceDetailResponseFromRecord 从数据库记录构造响应
func NewAttendanceDetailResponseFromRecord(
	record *model.AttendanceRecord,
	slotStart, slotEnd string,
	userMap map[uint]*model.User,
) *AttendanceDetailResponse {
	// 解析存储的JSON数据
	var onTimeStored []StoredUserCheck
	var leaveStored []StoredUserLeave
	var notArrivedIDs []uint

	json.Unmarshal([]byte(record.OnTimeIDs), &onTimeStored)
	json.Unmarshal([]byte(record.LeaveIDs), &leaveStored)
	json.Unmarshal([]byte(record.NotArrivedIDs), &notArrivedIDs)

	// 构建应到人员（所有出现在记录中的人员）
	shouldAttendMap := make(map[uint]bool)
	for _, u := range onTimeStored {
		shouldAttendMap[u.ID] = true
	}
	for _, u := range leaveStored {
		shouldAttendMap[u.ID] = true
	}
	for _, id := range notArrivedIDs {
		shouldAttendMap[id] = true
	}

	shouldAttendList := make([]AttendanceUserBasic, 0)
	for id := range shouldAttendMap {
		if user, ok := userMap[id]; ok {
			shouldAttendList = append(shouldAttendList, AttendanceUserBasic{
				ID:   user.ID,
				Name: user.Name,
			})
		}
	}

	// 构建正常打卡列表
	onTimeList := make([]AttendanceUserCheck, 0, len(onTimeStored))
	for _, u := range onTimeStored {
		if user, ok := userMap[u.ID]; ok {
			onTimeList = append(onTimeList, AttendanceUserCheck{
				ID:        user.ID,
				Name:      user.Name,
				CheckTime: time.Unix(u.CheckTime, 0),
			})
		}
	}

	// 构建请假列表
	leaveList := make([]AttendanceUserLeave, 0, len(leaveStored))
	for _, u := range leaveStored {
		if user, ok := userMap[u.ID]; ok {
			leaveList = append(leaveList, AttendanceUserLeave{
				ID:        user.ID,
				Name:      user.Name,
				LeaveType: u.LeaveType,
				Reason:    u.Reason,
			})
		}
	}

	// 构建未到列表
	notArrivedList := make([]AttendanceUserBasic, 0, len(notArrivedIDs))
	for _, id := range notArrivedIDs {
		if user, ok := userMap[id]; ok {
			notArrivedList = append(notArrivedList, AttendanceUserBasic{
				ID:   user.ID,
				Name: user.Name,
			})
		}
	}

	return &AttendanceDetailResponse{
		Date:    record.Date.Format("2006-01-02"),
		Week:    record.Week,
		Section: record.Section,
		SlotTime: SlotTimeInfo{
			Start: slotStart,
			End:   slotEnd,
		},
		Statistics: AttendanceStatistics{
			ShouldAttend: len(shouldAttendMap),
			OnTime:       len(onTimeStored),
			Leave:        len(leaveStored),
			NotArrived:   len(notArrivedIDs),
		},
		Users: AttendanceUserLists{
			ShouldAttend: shouldAttendList,
			OnTime:       onTimeList,
			Leave:        leaveList,
			NotArrived:   notArrivedList,
		},
	}
}
