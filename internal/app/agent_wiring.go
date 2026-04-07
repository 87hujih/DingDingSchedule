package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"schedule_server/config"
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

// ────────────── BuildAgent ──────────────

// BuildAgent 创建并返回一个 Agent 实例，组装所有适配器。
// customCallLog 仅用于脚本或测试场景覆盖默认日志写入。
func BuildAgent(
	repo *repository.Repository,
	scheduleSrv *service.ScheduleService,
	attendanceSrv *service.AttendanceRecordService,
	semesterSrv *service.SemesterService,
	schedulePeriodSrv *service.SchedulePeriodService,
	restDaySrv *service.RestDayService,
	leaveSyncSrv *service.LeaveSyncService,
	knowledgeSrv *service.AgentKnowledgeService,
	customCallLog agent.CallLogPort,
) *agent.Agent {
	cfg := global.AppConfig.LLM
	callLog := customCallLog
	if callLog == nil {
		callLog = &callLogAdapter{db: global.DB}
	}

	return agent.NewAgent(agent.Deps{
		LLMBaseURL:       cfg.BaseURL,
		LLMAPIKey:        cfg.APIKey,
		LLMModel:         cfg.Model,
		RouterLLMBaseURL: cfg.RouterBaseURL,
		RouterLLMAPIKey:  cfg.RouterAPIKey,
		RouterLLMModel:   cfg.RouterModel,
		RouteMode:        cfg.RouteMode,

		Schedule:        &scheduleAdapter{srv: scheduleSrv, schedulePeriodSrv: schedulePeriodSrv},
		Attendance:      &attendanceAdapter{srv: attendanceSrv, repo: repo.AttendanceRecordRepo},
		Leave:           &leaveAdapter{srv: leaveSyncSrv},
		User:            &userAdapter{repo: repo.UserRepo},
		Semester:        &semesterAdapter{srv: semesterSrv},
		SchedulePeriod:  &schedulePeriodAdapter{srv: schedulePeriodSrv},
		RestDay:         &restDayAdapter{srv: restDaySrv},
		GroupSub:        &groupSubAdapter{repo: repo.GroupSubRepo},
		Dept:            &deptAdapter{repo: repo.DeptRepo},
		Knowledge:       &knowledgeAdapter{srv: knowledgeSrv},
		CallLog:         callLog,
		AttendanceStats: attendanceSrv,
		UserCross:       attendanceSrv,
		Tenant:          &tenantAdapter{repo: repo.TenantRepo},

		Logger: global.Log,
	})
}

// ────────────── scheduleAdapter ──────────────

type scheduleAdapter struct {
	srv               scheduleQueryService
	schedulePeriodSrv *service.SchedulePeriodService
}

type scheduleQueryService interface {
	ListByWeek(ctx context.Context, viewerID uint, viewerRole int, targetUserID uint, week int) (*service.WeekScheduleResult, error)
	GetFreeUsersBySlot(ctx context.Context, week, dayStart, dayEnd int, deptID int64, periods []config.Period) ([]service.FreeUserSlot, error)
}

// ListMyScheduleByWeek 查询用户指定周次的个人课表，并转换为 Agent 可用的课程数据。
func (a *scheduleAdapter) ListMyScheduleByWeek(ctx context.Context, userID uint, week int) ([]agenttool.CourseItem, error) {
	result, err := a.srv.ListByWeek(ctx, userID, 0, userID, week) // viewerID=userID, viewerRole=0 (self)
	if err != nil {
		return nil, err
	}
	return convertScheduleCourses(result), nil
}

// ListUserScheduleByWeek 查询目标用户指定周次的课表，并转换为 Agent 可用的课程数据。
func (a *scheduleAdapter) ListUserScheduleByWeek(ctx context.Context, viewerID uint, viewerRole int, targetUserID uint, week int) ([]agenttool.CourseItem, error) {
	result, err := a.srv.ListByWeek(ctx, viewerID, viewerRole, targetUserID, week)
	if err != nil {
		return nil, err
	}
	return convertScheduleCourses(result), nil
}

// GetFreeUsersBySlot 查询指定周次和节次范围内的空闲人员列表。
func (a *scheduleAdapter) GetFreeUsersBySlot(ctx context.Context, week, dayStart, dayEnd int, deptID int64) ([]agenttool.FreeSlotResult, error) {
	// 获取当前活跃的作息时间配置
	info, err := a.schedulePeriodSrv.GetScheduleInfo(ctx)
	if err != nil {
		return nil, err
	}

	slots, err := a.srv.GetFreeUsersBySlot(ctx, week, dayStart, dayEnd, deptID, info.ActivePeriods)
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

func convertScheduleCourses(result *service.WeekScheduleResult) []agenttool.CourseItem {
	if result == nil {
		return nil
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
	return items
}

// ────────────── attendanceAdapter ──────────────

type attendanceAdapter struct {
	srv  attendanceDetailService
	repo repository.AttendanceRecordRepository
}

type attendanceDetailService interface {
	GetAttendanceDetail(ctx context.Context, req *dto.AttendanceDetailRequest) (*dto.AttendanceDetailResponse, error)
	GetAttendanceText(ctx context.Context, req *dto.AttendanceTextRequest) (*dto.AttendanceTextResponse, error)
	GetWeeklyRanking(ctx context.Context, req *dto.WeeklyAttendanceRankingRequest) (*dto.WeeklyAttendanceRankingResponse, error)
	GetWeeklyAttendanceRateRanking(ctx context.Context, req *dto.WeeklyAttendanceRankingRequest) (*dto.WeeklyAttendanceRateRankingResponse, error)
	SignForUsers(ctx context.Context, req *dto.SignForUserRequest) (*dto.SignForUserResponse, error)
}

// GetAttendanceDetail 查询指定条件下的考勤明细，并整理为 Agent 返回结构。
func (a *attendanceAdapter) GetAttendanceDetail(ctx context.Context, req agenttool.AttendanceQuery) (*agenttool.AttendanceResult, error) {
	dtoReq := &dto.AttendanceDetailRequest{
		Date:    req.Date,
		Week:    req.Week,
		Section: req.Section,
	}
	if req.DeptID != 0 {
		dtoReq.DeptIDs = []int64{req.DeptID}
	}
	resp, err := a.srv.GetAttendanceDetail(ctx, dtoReq)
	if err != nil {
		return nil, err
	}

	// 转换 OnTime 用户名
	onTimeNames := make([]string, 0, len(resp.Users.OnTime))
	for _, u := range resp.Users.OnTime {
		onTimeNames = append(onTimeNames, u.Name)
	}

	lateNames := make([]string, 0, len(resp.Users.Late))
	for _, u := range resp.Users.Late {
		lateNames = append(lateNames, u.Name)
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
		ViewMode:     resp.ViewMode,
		IsFinalized:  resp.IsFinalized,
		FinalizeAt:   resp.FinalizeAt.Format("2006-01-02 15:04:05"),
		ShouldAttend: resp.Statistics.ShouldAttend,
		OnTimeCount:  resp.Statistics.OnTime,
		LateCount:    resp.Statistics.Late,
		LeaveCount:   resp.Statistics.Leave,
		AbsentCount:  resp.Statistics.NotArrived,
		RestDayCount: resp.Statistics.RestDay,
		OnTimeUsers:  onTimeNames,
		LateUsers:    lateNames,
		LeaveUsers:   leaveUsers,
		AbsentUsers:  absentNames,
		RestDayUsers: restDayNames,
	}, nil
}

// GetAttendanceText 查询指定条件下的考勤文本摘要。
func (a *attendanceAdapter) GetAttendanceText(ctx context.Context, req agenttool.AttendanceQuery) (string, error) {
	dtoReq := &dto.AttendanceTextRequest{
		Date:    req.Date,
		Week:    req.Week,
		Section: req.Section,
	}
	if req.DeptID != 0 {
		dtoReq.DeptIDs = []int64{req.DeptID}
	}
	resp, err := a.srv.GetAttendanceText(ctx, dtoReq)
	if err != nil {
		return "", err
	}
	return resp.FullText, nil
}

// GetWeeklyAbsenceRanking 获取当前周的缺勤排名数据。
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

// GetWeeklyAttendanceRateRanking 获取当前周的出勤率排名数据。
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

// FindRecordByDateSection 按日期和节次查找考勤记录 ID，不存在时返回 0。
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

// SignForUsers 为指定考勤记录批量执行补签操作。
func (a *attendanceAdapter) SignForUsers(ctx context.Context, recordID uint, userIDs []uint) error {
	_, err := a.srv.SignForUsers(ctx, &dto.SignForUserRequest{
		RecordID:      recordID,
		TargetUserIDs: userIDs,
	})
	return err
}

// SignForUsersBySlot 为指定日期和节次批量执行补签，适用于实时阶段无快照记录的场景。
func (a *attendanceAdapter) SignForUsersBySlot(ctx context.Context, date string, section int, userIDs []uint) error {
	_, err := a.srv.SignForUsers(ctx, &dto.SignForUserRequest{
		Date:          date,
		Section:       section,
		TargetUserIDs: userIDs,
	})
	return err
}

// ────────────── leaveAdapter ──────────────

type leaveAdapter struct {
	srv *service.LeaveSyncService
}

// GetRecentLeaves 查询用户最近一段时间的请假记录，并转换为 Agent 展示格式。
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

// formatLeaveDuration 将请假起止时间转换为天数或小时的文本描述。
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

// FindByDingUserID 根据钉钉用户 ID 查询用户信息，不存在时返回 nil。
func (a *userAdapter) FindByDingUserID(ctx context.Context, dingUserID string) (*agenttool.UserInfo, error) {
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

// SearchByName 按姓名模糊搜索用户，并限制返回数量。
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

// GetCurrentWeek 计算当前学期所处周次，并返回学期总周数。
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

// GetScheduleInfo 获取当前启用的作息节次信息和模式名称。
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

// GetMyRestDay 查询用户的固定休息日信息。
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

// Subscribe 为群会话创建或更新考勤订阅配置。
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

// Unsubscribe 取消指定群会话的考勤订阅。
func (a *groupSubAdapter) Unsubscribe(ctx context.Context, tenantID uint, conversationID string) error {
	return a.repo.SoftDelete(ctx, tenantID, conversationID)
}

// GetSubscription 查询群会话当前的考勤订阅状态及配置。
func (a *groupSubAdapter) GetSubscription(ctx context.Context, tenantID uint, conversationID string) (*agenttool.GroupSubInfo, error) {
	sub, err := a.repo.FindByConversationID(ctx, tenantID, conversationID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return &agenttool.GroupSubInfo{Subscribed: false}, nil
	}

	info := &agenttool.GroupSubInfo{
		Subscribed: true,
		GroupName:  sub.GroupName,
		CreatedAt:  sub.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if sub.DeptIDsJSON != "" {
		var deptIDs []int64
		if err := json.Unmarshal([]byte(sub.DeptIDsJSON), &deptIDs); err == nil {
			info.DeptIDs = deptIDs
		}
	}
	return info, nil
}

// ────────────── deptAdapter ──────────────

type deptAdapter struct {
	repo repository.DepartmentRepository
}

// ListDepts 获取所有启用中的叶子部门列表。
func (a *deptAdapter) ListDepts(ctx context.Context) ([]agent.DeptItem, error) {
	depts, err := a.repo.FindLeaf(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]agent.DeptItem, 0, len(depts))
	for _, d := range depts {
		if d.Status != 1 {
			continue
		}
		items = append(items, agent.DeptItem{
			DeptID:   d.DeptID,
			Name:     d.Name,
			ParentID: d.ParentID,
		})
	}
	return items, nil
}

// ────────────── knowledgeAdapter ──────────────

type knowledgeAdapter struct {
	srv *service.AgentKnowledgeService
}

// Search 根据租户和问题检索规则知识切片。
func (a *knowledgeAdapter) Search(ctx context.Context, tenantID uint, query string, topK int) ([]agenttool.KnowledgeHit, error) {
	if a.srv == nil {
		return nil, nil
	}

	hits, err := a.srv.Search(ctx, tenantID, query, topK)
	if err != nil {
		return nil, err
	}

	result := make([]agenttool.KnowledgeHit, 0, len(hits))
	for _, hit := range hits {
		result = append(result, agenttool.KnowledgeHit{
			Title:      hit.Title,
			SourcePath: hit.SourcePath,
			DocType:    hit.DocType,
			Audience:   hit.Audience,
			Intent:     hit.Intent,
			ChunkIndex: hit.ChunkIndex,
			Heading:    hit.Heading,
			Body:       hit.Body,
			SourceRef:  hit.SourceRef,
			Score:      hit.Score,
		})
	}
	return result, nil
}

// ────────────── callLogAdapter ──────────────

type callLogAdapter struct {
	db *gorm.DB
}

// Write 记录一次 Agent 调用日志，并显式跳过租户作用域插件。
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
		TenantID:                log.TenantID,
		UserID:                  log.UserID,
		UserName:                log.UserName,
		ConvType:                log.ConvType,
		QueryType:               log.QueryType,
		ConversationEvent:       log.ConversationEvent,
		ActiveTaskType:          log.ActiveTaskType,
		TaskStatusBefore:        log.TaskStatusBefore,
		TaskStatusAfter:         log.TaskStatusAfter,
		DomainResult:            log.DomainResult,
		DomainHint:              log.DomainHint,
		PlanKind:                log.PlanKind,
		KnowledgeStrength:       log.KnowledgeStrength,
		PlannerReason:           log.PlannerReason,
		PlannerAction:           log.PlannerAction,
		PlannerConfidence:       log.PlannerConfidence,
		TaskID:                  log.TaskID,
		TaskKeepOpen:            log.TaskKeepOpen,
		TaskSwitch:              log.TaskSwitch,
		LastErrorCode:           log.LastErrorCode,
		ShadowPlannerAction:     log.ShadowPlannerAction,
		ShadowPlannerMatched:    log.ShadowPlannerMatched,
		RouteKind:               log.RouteKind,
		RouteConfidence:         log.RouteConfidence,
		RouteReasonCode:         log.RouteReasonCode,
		RouteSource:             log.RouteSource,
		ClarifyCode:             log.ClarifyCode,
		SoftNoticeCode:          log.SoftNoticeCode,
		ExecutorName:            log.ExecutorName,
		ToolPool:                log.ToolPool,
		RouterLatencyMs:         log.RouterLatencyMs,
		ExecutorLatencyMs:       log.ExecutorLatencyMs,
		ShadowRouteKind:         log.ShadowRouteKind,
		ShadowRouteMatched:      log.ShadowRouteMatched,
		AnswerMode:              log.AnswerMode,
		Question:                log.Question,
		ToolsCalled:             toolsCalled,
		ToolCallCount:           log.ToolCallCount,
		Reply:                   log.Reply,
		SourceRefs:              strings.Join(log.SourceRefs, ","),
		RetrievalHitCount:       log.RetrievalHitCount,
		RetrievalCandidateCount: log.RetrievalCandidateCount,
		RetrievalTopRefs:        strings.Join(log.RetrievalTopRefs, ","),
		RetrievalScores:         joinIntList(log.RetrievalScores),
		FollowUpMatchedSlots:    strings.Join(log.FollowUpMatchedSlots, ","),
		RetrievalFilteredReason: log.RetrievalFilteredReason,
		KnowledgeDocTypes:       strings.Join(log.KnowledgeDocTypes, ","),
		RetrievalDurationMs:     log.RetrievalDurationMs,
		LLMDurationMs:           log.LLMDurationMs,
		Rounds:                  log.Rounds,
		DurationMs:              log.DurationMs,
		Status:                  log.Status,
		ErrorMsg:                log.ErrorMsg,
	})
}

func joinIntList(values []int) string {
	if len(values) == 0 {
		return ""
	}

	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

// ────────────── tenantAdapter ──────────────

type tenantAdapter struct {
	repo repository.TenantRepository
}

// FindTenantIDByCorpID 根据企业 corpID 查找启用中的租户 ID。
func (a *tenantAdapter) FindTenantIDByCorpID(ctx context.Context, corpID string) (uint, error) {
	tenant, err := a.repo.FindActiveByCorpID(ctx, corpID)
	if errors.Is(err, repository.ErrTenantNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return tenant.ID, nil
}
