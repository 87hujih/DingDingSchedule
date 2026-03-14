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
	Subscribe(ctx context.Context, tenantID uint, conversationID, groupName string, enabledByUID uint) error
	Unsubscribe(ctx context.Context, tenantID uint, conversationID string) error
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

// PeriodInfo 作息时段信息
type PeriodInfo struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	End   string `json:"end"`
}
