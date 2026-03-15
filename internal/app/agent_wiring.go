package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"schedule_server/global"
	"schedule_server/internal/agent"
	agenttool "schedule_server/internal/agent/tools"
	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"
	"schedule_server/internal/tenantctx"

	"gorm.io/gorm"
)

// ────────────── buildAgent ──────────────

// buildAgent 创建并返回一个 Agent 实例，组装所有适配器
func buildAgent(
	repo *repository.Repository,
	scheduleSrv *service.ScheduleService,
	attendanceSrv *service.AttendanceRecordService,
	semesterSrv *service.SemesterService,
	schedulePeriodSrv *service.SchedulePeriodService,
	restDaySrv *service.RestDayService,
	leaveSyncSrv *service.LeaveSyncService,
) *agent.Agent {
	cfg := global.AppConfig.LLM

	return agent.NewAgent(agent.Deps{
		LLMBaseURL: cfg.BaseURL,
		LLMAPIKey:  cfg.APIKey,
		LLMModel:   cfg.Model,

		Schedule:       &scheduleAdapter{srv: scheduleSrv, schedulePeriodSrv: schedulePeriodSrv},
		Attendance:     &attendanceAdapter{srv: attendanceSrv, repo: repo.AttendanceRecordRepo},
		Leave:          &leaveAdapter{srv: leaveSyncSrv},
		User:           &userAdapter{repo: repo.UserRepo},
		Semester:       &semesterAdapter{srv: semesterSrv},
		SchedulePeriod: &schedulePeriodAdapter{srv: schedulePeriodSrv},
		RestDay:        &restDayAdapter{srv: restDaySrv},
		GroupSub:       &groupSubAdapter{repo: repo.GroupSubRepo},
		Dept:           &deptAdapter{repo: repo.DeptRepo},
		CallLog:        &callLogAdapter{db: global.DB},

		Logger: global.Log,
	})
}

// ────────────── scheduleAdapter ──────────────

type scheduleAdapter struct {
	srv               *service.ScheduleService
	schedulePeriodSrv *service.SchedulePeriodService
}

func (a *scheduleAdapter) ListMyScheduleByWeek(ctx context.Context, userID uint, week int) ([]agenttool.CourseItem, error) {
	result, err := a.srv.ListByWeek(ctx, userID, 0, userID, week) // viewerID=userID, viewerRole=0 (self)
	if err != nil {
		return nil, err
	}
	items := make([]agenttool.CourseItem, 0, len(result.Courses))
	for _, c := range result.Courses {
		items = append(items, agenttool.CourseItem{
			CourseName: c.CourseName,
			DayOfWeek:  c.DayOfWeek,
			Section:    c.Section,
			Location:   c.Location,
			Teacher:    c.Teacher,
			WeekList:   c.WeekList,
		})
	}
	return items, nil
}

func (a *scheduleAdapter) GetFreeUsersBySlot(ctx context.Context, week, dayStart, dayEnd int) ([]agenttool.FreeSlotResult, error) {
	// 获取当前活跃的作息时间配置
	info, err := a.schedulePeriodSrv.GetScheduleInfo(ctx)
	if err != nil {
		return nil, err
	}

	slots, err := a.srv.GetFreeUsersBySlot(ctx, week, dayStart, dayEnd, info.ActivePeriods)
	if err != nil {
		return nil, err
	}

	results := make([]agenttool.FreeSlotResult, 0, len(slots))
	for _, s := range slots {
		names := make([]string, 0, len(s.FreeUsers))
		for _, u := range s.FreeUsers {
			names = append(names, u.Name)
		}
		results = append(results, agenttool.FreeSlotResult{
			DayOfWeek: s.DayOfWeek,
			Section:   s.Section,
			SlotStart: s.SlotStart,
			SlotEnd:   s.SlotEnd,
			FreeUsers: names,
			FreeCount: len(names),
		})
	}
	return results, nil
}

// ────────────── attendanceAdapter ──────────────

type attendanceAdapter struct {
	srv  *service.AttendanceRecordService
	repo repository.AttendanceRecordRepository
}

func (a *attendanceAdapter) GetAttendanceDetail(ctx context.Context, req agenttool.AttendanceQuery) (*agenttool.AttendanceResult, error) {
	resp, err := a.srv.GetAttendanceRecordFromDB(ctx, &dto.AttendanceDetailRequest{
		Date:    req.Date,
		Week:    req.Week,
		Section: req.Section,
	})
	if err != nil {
		return nil, err
	}

	// 转换 OnTime 用户名
	onTimeNames := make([]string, 0, len(resp.Users.OnTime))
	for _, u := range resp.Users.OnTime {
		onTimeNames = append(onTimeNames, u.Name)
	}

	// 转换请假用户
	leaveUsers := make([]agenttool.AttendLeave, 0, len(resp.Users.Leave))
	for _, u := range resp.Users.Leave {
		leaveUsers = append(leaveUsers, agenttool.AttendLeave{
			Name:      u.Name,
			LeaveType: u.LeaveType,
		})
	}

	// 转换缺勤用户名
	absentNames := make([]string, 0, len(resp.Users.NotArrived))
	for _, u := range resp.Users.NotArrived {
		absentNames = append(absentNames, u.Name)
	}

	// 转换休息日用户名
	restDayNames := make([]string, 0, len(resp.Users.RestDay))
	for _, u := range resp.Users.RestDay {
		restDayNames = append(restDayNames, u.Name)
	}

	return &agenttool.AttendanceResult{
		Date:         resp.Date,
		Week:         resp.Week,
		Section:      resp.Section,
		SlotStart:    resp.SlotTime.Start,
		SlotEnd:      resp.SlotTime.End,
		ShouldAttend: resp.Statistics.ShouldAttend,
		OnTimeCount:  resp.Statistics.OnTime,
		LeaveCount:   resp.Statistics.Leave,
		AbsentCount:  resp.Statistics.NotArrived,
		RestDayCount: resp.Statistics.RestDay,
		OnTimeUsers:  onTimeNames,
		LeaveUsers:   leaveUsers,
		AbsentUsers:  absentNames,
		RestDayUsers: restDayNames,
	}, nil
}

func (a *attendanceAdapter) GetAttendanceText(ctx context.Context, req agenttool.AttendanceQuery) (string, error) {
	resp, err := a.srv.GetAttendanceText(ctx, &dto.AttendanceTextRequest{
		Date:    req.Date,
		Week:    req.Week,
		Section: req.Section,
	})
	if err != nil {
		return "", err
	}
	return resp.FullText, nil
}

func (a *attendanceAdapter) GetWeeklyAbsenceRanking(ctx context.Context) ([]agenttool.RankItem, error) {
	resp, err := a.srv.GetWeeklyRanking(ctx, &dto.WeeklyAttendanceRankingRequest{WeekOffset: 0})
	if err != nil {
		return nil, err
	}
	items := make([]agenttool.RankItem, 0, len(resp.Items))
	for _, it := range resp.Items {
		items = append(items, agenttool.RankItem{
			Name:  it.Name,
			Count: it.LateCount,
		})
	}
	return items, nil
}

func (a *attendanceAdapter) GetWeeklyAttendanceRateRanking(ctx context.Context) ([]agenttool.RankItem, error) {
	resp, err := a.srv.GetWeeklyAttendanceRateRanking(ctx, &dto.WeeklyAttendanceRankingRequest{WeekOffset: 0})
	if err != nil {
		return nil, err
	}
	items := make([]agenttool.RankItem, 0, len(resp.Items))
	for _, it := range resp.Items {
		items = append(items, agenttool.RankItem{
			Name:  it.Name,
			Count: it.OnTimeCount,
			Rate:  it.Rate,
		})
	}
	return items, nil
}

func (a *attendanceAdapter) FindRecordByDateSection(ctx context.Context, date string, section int) (uint, error) {
	t, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return 0, fmt.Errorf("日期格式错误: %w", err)
	}
	record, err := a.repo.FindByDateSection(ctx, t, section)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return record.ID, nil
}

func (a *attendanceAdapter) SignForUsers(ctx context.Context, recordID uint, userIDs []uint) error {
	_, err := a.srv.SignForUsers(ctx, &dto.SignForUserRequest{
		RecordID:      recordID,
		TargetUserIDs: userIDs,
	})
	return err
}

// ────────────── leaveAdapter ──────────────

type leaveAdapter struct {
	srv *service.LeaveSyncService
}

func (a *leaveAdapter) GetRecentLeaves(ctx context.Context, userID uint, days int) ([]agenttool.LeaveItem, error) {
	records, err := a.srv.GetRecentLeaves(ctx, userID, days)
	if err != nil {
		return nil, err
	}
	items := make([]agenttool.LeaveItem, 0, len(records))
	for _, r := range records {
		duration := formatLeaveDuration(r.StartAt, r.EndAt)
		items = append(items, agenttool.LeaveItem{
			Date:      r.StartAt.Format("2006-01-02"),
			LeaveType: r.LeaveType,
			Duration:  duration,
			Status:    r.Result,
		})
	}
	return items, nil
}

func formatLeaveDuration(start, end time.Time) string {
	hours := end.Sub(start).Hours()
	if hours >= 24 {
		days := int(hours / 24)
		return fmt.Sprintf("%d天", days)
	}
	return fmt.Sprintf("%.1f小时", hours)
}

// ────────────── userAdapter ──────────────

type userAdapter struct {
	repo repository.UserRepository
}

func (a *userAdapter) FindByDingUserID(ctx context.Context, dingUserID string) (*agenttool.UserInfo, error) {
	// Agent.Chat 在获取 tenantID 之前调用此方法，必须跳过租户隔离
	ctx = tenantctx.WithSkipTenantScope(ctx)
	user, err := a.repo.FindByDingUserID(ctx, dingUserID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &agenttool.UserInfo{
		ID:         user.ID,
		Name:       user.Name,
		DingUserID: user.DingUserID,
		Role:       user.Role,
		TenantID:   user.TenantID,
	}, nil
}

func (a *userAdapter) SearchByName(ctx context.Context, name string) ([]agenttool.UserInfo, error) {
	users, _, err := a.repo.SearchWithScope(ctx, name, nil, nil, 1, 10)
	if err != nil {
		return nil, err
	}
	items := make([]agenttool.UserInfo, 0, len(users))
	for _, u := range users {
		items = append(items, agenttool.UserInfo{
			ID:         u.ID,
			Name:       u.Name,
			DingUserID: u.DingUserID,
			Role:       u.Role,
			TenantID:   u.TenantID,
		})
	}
	return items, nil
}

// ────────────── semesterAdapter ──────────────

type semesterAdapter struct {
	srv *service.SemesterService
}

func (a *semesterAdapter) GetCurrentWeek(ctx context.Context) (int, int, error) {
	semester, err := a.srv.GetActiveSemester(ctx)
	if err != nil {
		return 0, 0, err
	}
	week, err := a.srv.CalculateWeekFromDate(semester, time.Now())
	if err != nil {
		return week, semester.TotalWeeks, err
	}
	return week, semester.TotalWeeks, nil
}

// ────────────── schedulePeriodAdapter ──────────────

type schedulePeriodAdapter struct {
	srv *service.SchedulePeriodService
}

func (a *schedulePeriodAdapter) GetScheduleInfo(ctx context.Context) ([]agenttool.PeriodInfo, string, error) {
	info, err := a.srv.GetScheduleInfo(ctx)
	if err != nil {
		return nil, "", err
	}
	periods := make([]agenttool.PeriodInfo, 0, len(info.ActivePeriods))
	for _, p := range info.ActivePeriods {
		periods = append(periods, agenttool.PeriodInfo{
			Name:  p.Name,
			Start: p.Start,
			End:   p.End,
		})
	}
	return periods, info.CurrentMode, nil
}

// ────────────── restDayAdapter ──────────────

type restDayAdapter struct {
	srv *service.RestDayService
}

func (a *restDayAdapter) GetMyRestDay(ctx context.Context, userID uint) (int, string, error) {
	resp, err := a.srv.GetMyRestDay(ctx, userID)
	if err != nil {
		return 0, "", err
	}
	return resp.DayOfWeek, resp.DayName, nil
}

// ────────────── groupSubAdapter ──────────────

type groupSubAdapter struct {
	repo repository.GroupAttendanceSubscriptionRepository
}

func (a *groupSubAdapter) Subscribe(ctx context.Context, tenantID uint, conversationID, groupName string, enabledByUID uint, deptIDs []int64) error {
	deptIDsJSON := ""
	if len(deptIDs) > 0 {
		b, err := json.Marshal(deptIDs)
		if err != nil {
			return err
		}
		deptIDsJSON = string(b)
	}
	return a.repo.Upsert(ctx, &model.GroupAttendanceSubscription{
		TenantID:       tenantID,
		ConversationID: conversationID,
		GroupName:      groupName,
		EnabledByUID:   enabledByUID,
		DeptIDsJSON:    deptIDsJSON,
	})
}

func (a *groupSubAdapter) Unsubscribe(ctx context.Context, tenantID uint, conversationID string) error {
	return a.repo.SoftDelete(ctx, tenantID, conversationID)
}

// ────────────── deptAdapter ──────────────

type deptAdapter struct {
	repo repository.DepartmentRepository
}

func (a *deptAdapter) ListDepts(ctx context.Context) ([]agent.DeptItem, error) {
	depts, err := a.repo.FindLeaf(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]agent.DeptItem, 0, len(depts))
	for _, d := range depts {
		items = append(items, agent.DeptItem{
			DeptID:   d.DeptID,
			Name:     d.Name,
			ParentID: d.ParentID,
		})
	}
	return items, nil
}

// ────────────── callLogAdapter ──────────────

type callLogAdapter struct {
	db *gorm.DB
}

func (a *callLogAdapter) Write(_ context.Context, log agenttool.CallLog) {
	toolsCalled := ""
	if len(log.ToolsCalled) > 0 {
		for i, t := range log.ToolsCalled {
			if i > 0 {
				toolsCalled += ","
			}
			toolsCalled += t
		}
	}
	// 使用 WithSkipTenantScope 跳过租户插件，TenantID 已在结构体中显式设置
	ctx := tenantctx.WithSkipTenantScope(context.Background())
	a.db.WithContext(ctx).Create(&model.AgentCallLog{
		TenantID:    log.TenantID,
		UserID:      log.UserID,
		UserName:    log.UserName,
		ConvType:    log.ConvType,
		Question:    log.Question,
		ToolsCalled: toolsCalled,
		Reply:       log.Reply,
		Rounds:      log.Rounds,
		DurationMs:  log.DurationMs,
		Status:      log.Status,
		ErrorMsg:    log.ErrorMsg,
	})
}
