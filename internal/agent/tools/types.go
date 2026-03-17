package tools

import (
	"context"
	"encoding/json"
)

// ────────────── LLM 通信类型 ──────────────

// Message 对话消息
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall LLM 返回的工具调用请求
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 工具调用的函数信息
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef 工具定义（发给 LLM 的 JSON Schema）
type ToolDef struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef 函数定义
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ────────────── Port 接口 ──────────────

// SchedulePort 课表能力
type SchedulePort interface {
	ListMyScheduleByWeek(ctx context.Context, userID uint, week int) ([]CourseItem, error)
	GetFreeUsersBySlot(ctx context.Context, week, dayStart, dayEnd int) ([]FreeSlotResult, error)
}

// AttendancePort 考勤能力
type AttendancePort interface {
	GetAttendanceDetail(ctx context.Context, req AttendanceQuery) (*AttendanceResult, error)
	GetAttendanceText(ctx context.Context, req AttendanceQuery) (string, error)
	GetWeeklyAbsenceRanking(ctx context.Context) ([]RankItem, error)
	GetWeeklyAttendanceRateRanking(ctx context.Context) ([]RankItem, error)
	FindRecordByDateSection(ctx context.Context, date string, section int) (uint, error)
	SignForUsers(ctx context.Context, recordID uint, userIDs []uint) error
}

// LeavePort 请假能力
type LeavePort interface {
	GetRecentLeaves(ctx context.Context, userID uint, days int) ([]LeaveItem, error)
}

// UserPort 用户能力
type UserPort interface {
	FindByDingUserID(ctx context.Context, dingUserID string) (*UserInfo, error)
	SearchByName(ctx context.Context, name string) ([]UserInfo, error)
}

// SemesterPort 学期能力
type SemesterPort interface {
	GetCurrentWeek(ctx context.Context) (week int, totalWeeks int, err error)
}

// SchedulePeriodPort 作息时间能力
type SchedulePeriodPort interface {
	GetScheduleInfo(ctx context.Context) ([]PeriodInfo, string, error) // periods, mode, err
}

// RestDayPort 休息日能力
type RestDayPort interface {
	GetMyRestDay(ctx context.Context, userID uint) (dayOfWeek int, dayName string, err error)
}

// GroupSubPort 群订阅能力
type GroupSubPort interface {
	Subscribe(ctx context.Context, tenantID uint, conversationID, groupName string, enabledByUID uint, deptIDs []int64) error
	Unsubscribe(ctx context.Context, tenantID uint, conversationID string) error
	GetSubscription(ctx context.Context, tenantID uint, conversationID string) (*GroupSubInfo, error)
}

// GroupSubInfo 群订阅状态
type GroupSubInfo struct {
	Subscribed bool    `json:"subscribed"`
	GroupName  string  `json:"group_name,omitempty"`
	DeptIDs    []int64 `json:"dept_ids,omitempty"` // 空表示推送全部人员
	CreatedAt  string  `json:"created_at,omitempty"`
}

// DeptPort 部门查询能力
type DeptPort interface {
	ListDepts(ctx context.Context) ([]DeptItem, error)
}

// ────────────── Agent 数据类型 ──────────────

// CourseItem 课程条目
type CourseItem struct {
	CourseName string `json:"course_name"`
	DayOfWeek  int    `json:"day_of_week"`
	Section    int    `json:"section"`
	Location   string `json:"location"`
	Teacher    string `json:"teacher"`
	WeekList   string `json:"week_list"`
}

// AttendanceQuery 考勤查询参数
type AttendanceQuery struct {
	Date    string
	Week    int
	Section int
	DeptID  int64 // 0 表示不限部门
}

// AttendanceResult 考勤查询结果
type AttendanceResult struct {
	Date         string        `json:"date"`
	Week         int           `json:"week"`
	Section      int           `json:"section"`
	SlotStart    string        `json:"slot_start"`
	SlotEnd      string        `json:"slot_end"`
	ShouldAttend int           `json:"should_attend"`
	OnTimeCount  int           `json:"on_time_count"`
	LeaveCount   int           `json:"leave_count"`
	AbsentCount  int           `json:"absent_count"`
	RestDayCount int           `json:"rest_day_count"`
	OnTimeUsers  []string      `json:"on_time_users"`
	LeaveUsers   []AttendLeave `json:"leave_users"`
	AbsentUsers  []string      `json:"absent_users"`
	RestDayUsers []string      `json:"rest_day_users"`
}

// AttendLeave 请假用户信息
type AttendLeave struct {
	Name      string `json:"name"`
	LeaveType string `json:"leave_type"`
}

// FreeSlotResult 无课人员查询结果
type FreeSlotResult struct {
	DayOfWeek int      `json:"day_of_week"`
	Section   int      `json:"section"`
	SlotStart string   `json:"slot_start"`
	SlotEnd   string   `json:"slot_end"`
	FreeUsers []string `json:"free_users"`
	FreeCount int      `json:"free_count"`
}

// RankItem 排行条目
type RankItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Rate  string `json:"rate,omitempty"`
}

// LeaveItem 请假条目
type LeaveItem struct {
	Date      string `json:"date"`
	LeaveType string `json:"leave_type"`
	Duration  string `json:"duration"`
	Status    string `json:"status"`
}

// UserInfo 用户基本信息
type UserInfo struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	DingUserID string `json:"ding_user_id"`
	Role       int    `json:"role"`
	TenantID   uint   `json:"tenant_id"`
}

// CallLog 一次对话的调用记录
type CallLog struct {
	TenantID    uint
	UserID      uint
	UserName    string
	ConvType    string // "1"=单聊, "2"=群聊
	Question    string
	ToolsCalled []string // 调用的工具名列表
	Reply       string
	Rounds      int
	DurationMs  int64
	Status      string // success / failed / timeout
	ErrorMsg    string
}

// CallLogPort 调用日志写入能力
type CallLogPort interface {
	Write(ctx context.Context, log CallLog)
}

// TenantPort 租户查询能力
type TenantPort interface {
	// FindTenantIDByCorpID 根据钉钉企业 corpID 查找对应的租户 ID，未找到时返回 0, nil
	FindTenantIDByCorpID(ctx context.Context, corpID string) (uint, error)
}

// ────────────── 通用查询层类型 ──────────────

// AttendanceStatsQuery 考勤统计查询参数
type AttendanceStatsQuery struct {
	// 时间维度：DateRange > Date > WeekRange > Week，优先级依次降低
	Week      int       // 单周
	WeekRange [2]int    // 周次范围 [起始周, 结束周]，两值都 > 0 时生效
	Date      string    // 单日 YYYY-MM-DD
	DateRange [2]string // 日期范围 [开始, 结束]

	// 节次维度
	Section  int   // 单节次
	Sections []int // 多节次，与 Section 二选一

	// 人员/部门筛选
	UserName string
	DeptID   int64 // 0 表示不限

	// 聚合维度
	GroupBy string // "user"|"dept"|"week"|"section"|"day"，不填返回原始明细

	// 聚合后过滤（HAVING）
	MinAbsentCount int     // 缺勤次数 >= N
	MaxOnTimeRate  float64 // 出勤率 <= 0.x（0 表示不限）

	// 排序
	SortBy    string // "absent_count"|"on_time_count"|"on_time_rate"|"leave_count"
	SortOrder string // "asc"|"desc"
	Limit     int    // 默认 20
}

// AttendanceStatItem 聚合统计条目
type AttendanceStatItem struct {
	Label       string `json:"label"` // 分组标签
	AbsentCount int    `json:"absent_count"`
	OnTimeCount int    `json:"on_time_count"`
	LeaveCount  int    `json:"leave_count"`
	TotalCount  int    `json:"total_count"`
	OnTimeRate  string `json:"on_time_rate"` // 如 "85.0%"
}

// AttendanceStatsPort 考勤统计查询能力
type AttendanceStatsPort interface {
	QueryStats(ctx context.Context, req AttendanceStatsQuery) ([]AttendanceStatItem, error)
}

// SlotCondition 时间槽条件
type SlotCondition struct {
	Week      int // 0 表示不限周次
	DayOfWeek int // 1-7，必填
	Section   int // 必填
}

// AbsentCondition 缺勤条件（Date 与 Week 二选一）
type AbsentCondition struct {
	Date    string // YYYY-MM-DD
	Week    int    // 周次，与 Date 二选一
	Section int    // 必填
}

// UserCrossQuery 人员交叉查询参数
type UserCrossQuery struct {
	FreeSlots []SlotCondition   // AND 语义：同时在所有槽无课
	BusySlots []SlotCondition   // AND 语义：同时在所有槽有课
	AbsentOn  []AbsentCondition // OR 语义：命中任意一条缺勤即满足
	DeptID    int64             // 0 表示不限
	UserNames []string          // 非空时只在这些人中查找
}

// UserCrossPort 人员交叉查询能力
type UserCrossPort interface {
	QueryUserCross(ctx context.Context, req UserCrossQuery) ([]string, error)
}

// PeriodInfo 作息时段信息
type PeriodInfo struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// DeptItem 部门信息
type DeptItem struct {
	DeptID   int64  `json:"dept_id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parent_id"`
}
