package agent

import "schedule_server/internal/agent/tools"

// Port 接口类型别名，便于外部引用 agent.SchedulePort 等
type (
	SchedulePort       = tools.SchedulePort
	AttendancePort     = tools.AttendancePort
	LeavePort          = tools.LeavePort
	UserPort           = tools.UserPort
	SemesterPort       = tools.SemesterPort
	SchedulePeriodPort = tools.SchedulePeriodPort
	RestDayPort        = tools.RestDayPort
	GroupSubPort       = tools.GroupSubPort
	DeptPort           = tools.DeptPort
	CallLogPort        = tools.CallLogPort
)

// 数据类型别名，便于外部引用 agent.CourseItem 等
type (
	CourseItem       = tools.CourseItem
	AttendanceQuery  = tools.AttendanceQuery
	AttendanceResult = tools.AttendanceResult
	AttendLeave      = tools.AttendLeave
	FreeSlotResult   = tools.FreeSlotResult
	RankItem         = tools.RankItem
	LeaveItem        = tools.LeaveItem
	UserInfo         = tools.UserInfo
	PeriodInfo       = tools.PeriodInfo
	DeptItem         = tools.DeptItem
)

// LLM 通信类型别名
type (
	Message      = tools.Message
	ToolCall     = tools.ToolCall
	FunctionCall = tools.FunctionCall
	ToolDef      = tools.ToolDef
	FunctionDef  = tools.FunctionDef
)
