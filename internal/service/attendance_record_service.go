package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"schedule_server/config"
	"schedule_server/internal/dto"
	"schedule_server/internal/errs"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/dingtalk"
	"schedule_server/pkg/scheduleutil"
	"schedule_server/pkg/weekutil"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// AttendanceRecordService 考勤记录服务（打卡统计）
type AttendanceRecordService struct {
	userRepo               repository.UserRepository
	courseRepo             repository.CourseRepository
	leaveRepo              repository.LeaveApprovalRepository
	attendanceRecordRepo   repository.AttendanceRecordRepository
	manualOverrideRepo     repository.AttendanceManualOverrideRepository
	scheduleSettingRepo    repository.ScheduleSettingRepository
	restDayRepo            repository.UserRestDayRepository
	dingMgr                *DingTalkClientManager
	scheduleCfg            config.Schedule        // 配置文件作为回退
	schedulePeriodSrv      *SchedulePeriodService // 从数据库读取作息时间
	semesterSrv            *SemesterService       // 学期服务（用于判断全体应到模式）
	logger                 *zap.SugaredLogger
	nowFn                  func() time.Time
	fetchAttendanceRecords func(ctx context.Context, dingUserIDs []string, startAt, endAt time.Time) ([]dingtalk.CheckRecord, error)
}

type attendanceWindow struct {
	date         time.Time
	periods      []config.Period
	slotStart    time.Time
	slotEnd      time.Time
	lateDeadline time.Time
	finalizeAt   time.Time
}

const attendanceOverrideTypeForceOnTime = "force_on_time"

type attendanceSignSlot struct {
	date    time.Time
	week    int
	section int
	record  *model.AttendanceRecord
}

// NewAttendanceRecordService 创建考勤记录服务实例
func NewAttendanceRecordService(
	userRepo repository.UserRepository,
	courseRepo repository.CourseRepository,
	leaveRepo repository.LeaveApprovalRepository,
	attendanceRecordRepo repository.AttendanceRecordRepository,
	manualOverrideRepo repository.AttendanceManualOverrideRepository,
	scheduleSettingRepo repository.ScheduleSettingRepository,
	restDayRepo repository.UserRestDayRepository,
	dingMgr *DingTalkClientManager,
	schedulePeriodSrv *SchedulePeriodService,
	semesterSrv *SemesterService,
	scheduleCfg config.Schedule,
	logger *zap.SugaredLogger,
) *AttendanceRecordService {
	return &AttendanceRecordService{
		userRepo:             userRepo,
		courseRepo:           courseRepo,
		leaveRepo:            leaveRepo,
		attendanceRecordRepo: attendanceRecordRepo,
		manualOverrideRepo:   manualOverrideRepo,
		scheduleSettingRepo:  scheduleSettingRepo,
		restDayRepo:          restDayRepo,
		dingMgr:              dingMgr,
		schedulePeriodSrv:    schedulePeriodSrv,
		semesterSrv:          semesterSrv,
		scheduleCfg:          scheduleCfg,
		logger:               logger,
		nowFn:                time.Now,
	}
}
func (s *AttendanceRecordService) currentTime() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func (s *AttendanceRecordService) calculateFinalizeAt(slotStart time.Time) time.Time {
	return slotStart.Add(30 * time.Minute)
}

func (s *AttendanceRecordService) shouldUseRealtimeView(now, finalizeAt time.Time) bool {
	return now.Before(finalizeAt)
}

func (s *AttendanceRecordService) resolveAttendanceWindow(
	ctx context.Context,
	req *dto.AttendanceDetailRequest,
) (*attendanceWindow, error) {
	date, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
	if err != nil {
		return nil, response.ErrInvalidParamWithMsg("日期格式错误")
	}

	periods := s.resolveActivePeriods(ctx)
	if req.Section <= 0 || req.Section > len(periods) {
		return nil, response.ErrInvalidParamWithMsg("节次无效")
	}

	slotStart, slotEnd, err := scheduleutil.CalculateSlotTime(date, req.Section, periods)
	if err != nil {
		return nil, response.NewBizError(response.CodeInternalError, err.Error())
	}

	return &attendanceWindow{
		date:         date,
		periods:      periods,
		slotStart:    slotStart,
		slotEnd:      slotEnd,
		lateDeadline: slotStart.Add(time.Duration(s.scheduleCfg.LateGraceMinutes) * time.Minute),
		finalizeAt:   s.calculateFinalizeAt(slotStart),
	}, nil
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

// GetAttendanceDetail 获取考勤详情（核心方法）
// 查询指定日期、周次、节次的考勤情况，包括应到、正常打卡、请假、未到人员
func (s *AttendanceRecordService) GetAttendanceDetail(
	ctx context.Context,
	req *dto.AttendanceDetailRequest,
) (*dto.AttendanceDetailResponse, error) {
	window, err := s.resolveAttendanceWindow(ctx, req)
	if err != nil {
		return nil, err
	}

	now := s.currentTime()
	if s.shouldUseRealtimeView(now, window.finalizeAt) {
		resp, _, err := s.buildAttendanceDetail(ctx, req, window, minTime(now, minTime(window.finalizeAt, window.slotEnd)), true)
		if err != nil {
			return nil, err
		}
		resp, err = s.applyManualOverridesToDetail(ctx, window.date, req.Section, resp)
		if err != nil {
			return nil, err
		}
		resp.SetViewMetadata("current", false, window.finalizeAt)
		return resp, nil
	}

	return s.GetAttendanceRecordFromDB(ctx, req)
}

func (s *AttendanceRecordService) GetAttendanceDetailWithLateUsers(
	ctx context.Context,
	req *dto.AttendanceDetailRequest,
) (*dto.AttendanceDetailResponse, []model.User, error) {
	return s.getAttendanceDetailWithLateUsers(ctx, req)
}

func (s *AttendanceRecordService) getAttendanceDetailWithLateUsers(
	ctx context.Context,
	req *dto.AttendanceDetailRequest,
) (*dto.AttendanceDetailResponse, []model.User, error) {
	// 1. 解析日期
	date, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
	if err != nil {
		return nil, nil, response.ErrInvalidParamWithMsg("日期格式错误")
	}

	// 2. 从数据库获取当前生效的作息时间配置
	periods := s.resolveActivePeriods(ctx)

	// 3. 校验节次
	if req.Section <= 0 || req.Section > len(periods) {
		return nil, nil, response.ErrInvalidParamWithMsg("节次无效")
	}

	// 4. 计算时间窗口
	slotStart, slotEnd, err := scheduleutil.CalculateSlotTime(date, req.Section, periods)
	if err != nil {
		return nil, nil, response.NewBizError(response.CodeInternalError, err.Error())
	}

	// 5. 获取考勤候选人与原始有课人员
	activeUsers, hasCourseUsers, err := s.getAttendanceCandidatesWithCourseUsers(ctx, date, req.Week, req.Section, req.DeptIDs)
	if err != nil {
		return nil, nil, err
	}

	// 5.5 先处理休息日：rest_day > leave > has_course
	dayOfWeek := scheduleutil.WeekdayMon1Sun7(date)
	restDayUsers := s.filterRestDayUsers(ctx, activeUsers, dayOfWeek)
	restDaySet := userIDBoolSet(restDayUsers)
	nonRestUsers := filterModelUsers(activeUsers, restDaySet)

	// 5.6 再处理请假：leave > has_course
	leave, err := s.getLeaveUsers(ctx, nonRestUsers, slotStart, slotEnd)
	if err != nil {
		return nil, nil, err
	}
	leaveSet := attendanceLeaveIDBoolSet(leave)

	filteredHasCourseUsers := filterModelUsers(hasCourseUsers, mergeBoolSets(restDaySet, leaveSet))
	hasCourseBasic := toBasicList(filteredHasCourseUsers)
	hasCourseSet := userIDBoolSet(filteredHasCourseUsers)
	shouldAttend := filterModelUsers(activeUsers, mergeBoolSets(restDaySet, hasCourseSet))

	if len(shouldAttend) == 0 {
		// 构建休息日用户基础信息
		restDayBasic := toRestDayBasicList(restDayUsers)
		return dto.NewAttendanceDetailResponse(
			req.Date, req.Week, req.Section,
			periods[req.Section-1].Start,
			periods[req.Section-1].End,
			shouldAttend,
			[]dto.AttendanceUserCheck{},
			[]dto.AttendanceUserLeave{},
			[]dto.AttendanceUserBasic{},
			restDayBasic,
			hasCourseBasic,
		), nil, nil
	}

	// 6. 获取打卡记录（只返回正常打卡的人）
	lateDeadline := slotStart.Add(time.Duration(s.scheduleCfg.LateGraceMinutes) * time.Minute)
	onTime, _, err := s.getOnTimeUsers(ctx, shouldAttend, date, req.Section, lateDeadline, slotEnd)
	if err != nil {
		return nil, nil, err
	}

	// 6.5 连续节次打卡顺延
	onTime = s.applyCarryForward(ctx, date, req.Section, periods, shouldAttend, onTime)

	// 7.5 优先级处理：请假 > 已到，休息 > 已到，从 onTime 中移除对应用户
	onTime = filterAttendanceChecks(onTime, mergeBoolSets(leaveSet, restDaySet))

	// 8. 计算未到人员（应到 - 正常打卡 - 请假）
	notArrived := s.calculateNotArrived(shouldAttend, onTime, leave)

	// 9. 需要通知的人员：应到但未正常打卡且未请假（含迟到和缺勤）
	notifyUsers := buildNotifyList(shouldAttend, onTime, leave)

	// 10. 构建响应（包含休息日用户）
	restDayBasic := toRestDayBasicList(restDayUsers)
	return dto.NewAttendanceDetailResponse(
		req.Date, req.Week, req.Section,
		periods[req.Section-1].Start,
		periods[req.Section-1].End,
		shouldAttend, onTime, leave, notArrived, restDayBasic, hasCourseBasic,
	), notifyUsers, nil
}

func (s *AttendanceRecordService) buildAttendanceDetail(
	ctx context.Context,
	req *dto.AttendanceDetailRequest,
	window *attendanceWindow,
	attendanceEnd time.Time,
	splitLate bool,
) (*dto.AttendanceDetailResponse, []model.User, error) {
	if window == nil {
		return nil, nil, response.NewBizError(response.CodeInternalError, "考勤时间窗口未初始化")
	}

	activeUsers, hasCourseUsers, err := s.getAttendanceCandidatesWithCourseUsers(ctx, window.date, req.Week, req.Section, req.DeptIDs)
	if err != nil {
		return nil, nil, err
	}

	dayOfWeek := scheduleutil.WeekdayMon1Sun7(window.date)
	restDayUsers := s.filterRestDayUsers(ctx, activeUsers, dayOfWeek)
	restDaySet := userIDBoolSet(restDayUsers)
	nonRestUsers := filterModelUsers(activeUsers, restDaySet)

	leave, err := s.getLeaveUsers(ctx, nonRestUsers, window.slotStart, window.slotEnd)
	if err != nil {
		return nil, nil, err
	}
	leaveSet := attendanceLeaveIDBoolSet(leave)

	filteredHasCourseUsers := filterModelUsers(hasCourseUsers, mergeBoolSets(restDaySet, leaveSet))
	hasCourseBasic := toBasicList(filteredHasCourseUsers)
	hasCourseSet := userIDBoolSet(filteredHasCourseUsers)
	shouldAttend := filterModelUsers(activeUsers, mergeBoolSets(restDaySet, hasCourseSet))
	restDayBasic := toRestDayBasicList(restDayUsers)
	if len(shouldAttend) == 0 {
		resp := dto.NewAttendanceDetailResponse(
			req.Date, req.Week, req.Section,
			window.periods[req.Section-1].Start,
			window.periods[req.Section-1].End,
			shouldAttend,
			[]dto.AttendanceUserCheck{},
			[]dto.AttendanceUserLeave{},
			[]dto.AttendanceUserBasic{},
			restDayBasic,
			hasCourseBasic,
		)
		return resp, nil, nil
	}

	onTime, late, err := s.getOnTimeUsers(ctx, shouldAttend, window.date, req.Section, window.lateDeadline, attendanceEnd)
	if err != nil {
		return nil, nil, err
	}
	onTime = s.applyCarryForward(ctx, window.date, req.Section, window.periods, shouldAttend, onTime)

	excluded := mergeBoolSets(leaveSet, restDaySet)

	onTime = filterAttendanceChecks(onTime, excluded)
	late = filterAttendanceChecks(late, excluded)

	notArrived := s.calculateNotArrived(shouldAttend, onTime, leave)
	if splitLate {
		notArrived = s.calculateNotArrivedWithLate(shouldAttend, onTime, late, leave)
	}
	notifyUsers := buildNotifyList(shouldAttend, onTime, leave)

	resp := dto.NewAttendanceDetailResponse(
		req.Date, req.Week, req.Section,
		window.periods[req.Section-1].Start,
		window.periods[req.Section-1].End,
		shouldAttend,
		onTime,
		leave,
		notArrived,
		restDayBasic,
		hasCourseBasic,
	)
	if splitLate {
		resp.Users.Late = late
		resp.Statistics.Late = len(late)
	}

	return resp, notifyUsers, nil
}

func normalizeAttendanceDate(date time.Time) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
}

func (s *AttendanceRecordService) deriveAttendanceWeek(ctx context.Context, date time.Time) int {
	if s.semesterSrv != nil {
		semester, err := s.semesterSrv.GetActiveSemester(ctx)
		if err == nil {
			if week, err := s.semesterSrv.CalculateWeekFromDate(semester, date); err == nil && week > 0 {
				return week
			}
		}
	}
	return 1
}

func (s *AttendanceRecordService) resolveSignSlot(ctx context.Context, req *dto.SignForUserRequest) (*attendanceSignSlot, error) {
	if req == nil {
		return nil, response.ErrInvalidParamWithMsg("请求参数不能为空")
	}
	if err := req.Validate(); err != nil {
		return nil, response.ErrInvalidParamWithMsg(err.Error())
	}

	if req.RecordID != 0 {
		record, err := s.attendanceRecordRepo.FindByID(ctx, req.RecordID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, response.ErrNotFoundWithMsg("未找到考勤记录")
			}
			return nil, err
		}
		return &attendanceSignSlot{
			date:    normalizeAttendanceDate(record.Date),
			week:    record.Week,
			section: record.Section,
			record:  record,
		}, nil
	}

	date, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
	if err != nil {
		return nil, response.ErrInvalidParamWithMsg("日期格式错误")
	}
	date = normalizeAttendanceDate(date)

	periods := s.resolveActivePeriods(ctx)
	if req.Section <= 0 || req.Section > len(periods) {
		return nil, response.ErrInvalidParamWithMsg("节次无效")
	}

	slot := &attendanceSignSlot{
		date:    date,
		week:    s.deriveAttendanceWeek(ctx, date),
		section: req.Section,
	}
	record, err := s.attendanceRecordRepo.FindByDateSection(ctx, date, req.Section)
	if err == nil {
		slot.record = record
		if record.Week > 0 {
			slot.week = record.Week
		}
		return slot, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	return slot, nil
}

func (s *AttendanceRecordService) loadAttendanceDetailForSign(ctx context.Context, slot *attendanceSignSlot) (*dto.AttendanceDetailResponse, error) {
	if slot == nil {
		return nil, response.NewBizError(response.CodeInternalError, "代签节次未初始化")
	}

	req := &dto.AttendanceDetailRequest{
		Date:    slot.date.Format("2006-01-02"),
		Week:    slot.week,
		Section: slot.section,
	}
	if slot.record != nil {
		return s.GetAttendanceRecordFromDB(ctx, req)
	}

	window, err := s.resolveAttendanceWindow(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, _, err := s.buildAttendanceDetail(ctx, req, window, minTime(s.currentTime(), minTime(window.finalizeAt, window.slotEnd)), true)
	if err != nil {
		return nil, err
	}
	return s.applyManualOverridesToDetail(ctx, slot.date, slot.section, resp)
}

func (s *AttendanceRecordService) resolveOverrideTenantID(
	ctx context.Context,
	slot *attendanceSignSlot,
	targetUserIDs []uint,
) uint {
	if slot != nil && slot.record != nil && slot.record.TenantID != 0 {
		return slot.record.TenantID
	}
	if tenantID, ok := tenantctx.TenantIDFrom(ctx); ok {
		return tenantID
	}
	if s.userRepo == nil || len(targetUserIDs) == 0 {
		return 0
	}

	users, err := s.userRepo.ListByIDs(ctx, targetUserIDs)
	if err != nil || len(users) == 0 {
		return 0
	}
	return users[0].TenantID
}

func (s *AttendanceRecordService) applyManualOverridesToDetail(
	ctx context.Context,
	date time.Time,
	section int,
	resp *dto.AttendanceDetailResponse,
) (*dto.AttendanceDetailResponse, error) {
	if resp == nil || s.manualOverrideRepo == nil {
		return resp, nil
	}

	overrides, err := s.manualOverrideRepo.ListByDateSection(ctx, normalizeAttendanceDate(date), section)
	if err != nil {
		return nil, errs.WrapMsgErr("获取人工代签覆盖失败", err)
	}
	if len(overrides) == 0 {
		return resp, nil
	}

	sourceByID := make(map[uint]dto.AttendanceUserBasic)
	allowed := make(map[uint]bool)

	addBasic := func(user dto.AttendanceUserBasic) {
		sourceByID[user.ID] = user
		allowed[user.ID] = true
	}
	addCheck := func(user dto.AttendanceUserCheck) {
		sourceByID[user.ID] = dto.AttendanceUserBasic{
			ID:       user.ID,
			Name:     user.Name,
			Avatar:   user.Avatar,
			DeptName: user.DeptName,
		}
		allowed[user.ID] = true
	}
	addLeave := func(user dto.AttendanceUserLeave) {
		if _, ok := sourceByID[user.ID]; ok {
			return
		}
		sourceByID[user.ID] = dto.AttendanceUserBasic{
			ID:       user.ID,
			Name:     user.Name,
			Avatar:   user.Avatar,
			DeptName: user.DeptName,
		}
	}

	for _, user := range resp.Users.ShouldAttend {
		addBasic(user)
	}
	for _, user := range resp.Users.OnTime {
		addCheck(user)
	}
	for _, user := range resp.Users.Late {
		addCheck(user)
	}
	for _, user := range resp.Users.NotArrived {
		addBasic(user)
	}

	excluded := make(map[uint]bool, len(resp.Users.Leave)+len(resp.Users.RestDay)+len(resp.Users.HasCourse))
	for _, user := range resp.Users.Leave {
		excluded[user.ID] = true
		addLeave(user)
	}
	for _, user := range resp.Users.RestDay {
		excluded[user.ID] = true
		if _, ok := sourceByID[user.ID]; !ok {
			sourceByID[user.ID] = user
		}
	}
	for _, user := range resp.Users.HasCourse {
		excluded[user.ID] = true
		if _, ok := sourceByID[user.ID]; !ok {
			sourceByID[user.ID] = user
		}
	}

	onTimeIndex := make(map[uint]int, len(resp.Users.OnTime))
	for i, user := range resp.Users.OnTime {
		onTimeIndex[user.ID] = i
	}

	applied := false
	for _, override := range overrides {
		if override.OverrideType != attendanceOverrideTypeForceOnTime {
			continue
		}
		if excluded[override.UserID] || !allowed[override.UserID] {
			continue
		}

		excludedUser := map[uint]bool{override.UserID: true}
		resp.Users.Late = filterAttendanceChecks(resp.Users.Late, excludedUser)
		resp.Users.NotArrived = filterAttendanceBasics(resp.Users.NotArrived, excludedUser)

		if idx, ok := onTimeIndex[override.UserID]; ok {
			resp.Users.OnTime[idx].CheckTime = override.AppliedAt
		} else {
			source := sourceByID[override.UserID]
			resp.Users.OnTime = append(resp.Users.OnTime, dto.AttendanceUserCheck{
				ID:        source.ID,
				Name:      source.Name,
				Avatar:    source.Avatar,
				DeptName:  source.DeptName,
				CheckTime: override.AppliedAt,
			})
			onTimeIndex[override.UserID] = len(resp.Users.OnTime) - 1
		}

		applied = true
	}

	if !applied {
		return resp, nil
	}

	resp.Statistics.OnTime = len(resp.Users.OnTime)
	resp.Statistics.Late = len(resp.Users.Late)
	resp.Statistics.NotArrived = len(resp.Users.NotArrived)
	return resp, nil
}

func filterAttendanceBasics(users []dto.AttendanceUserBasic, excluded map[uint]bool) []dto.AttendanceUserBasic {
	if len(excluded) == 0 {
		return users
	}
	filtered := make([]dto.AttendanceUserBasic, 0, len(users))
	for _, user := range users {
		if !excluded[user.ID] {
			filtered = append(filtered, user)
		}
	}
	return filtered
}

func filterModelUsers(users []model.User, excluded map[uint]bool) []model.User {
	if len(excluded) == 0 {
		return users
	}
	filtered := make([]model.User, 0, len(users))
	for _, user := range users {
		if !excluded[user.ID] {
			filtered = append(filtered, user)
		}
	}
	return filtered
}

func userIDBoolSet(users []model.User) map[uint]bool {
	set := make(map[uint]bool, len(users))
	for _, user := range users {
		set[user.ID] = true
	}
	return set
}

func attendanceLeaveIDBoolSet(users []dto.AttendanceUserLeave) map[uint]bool {
	set := make(map[uint]bool, len(users))
	for _, user := range users {
		set[user.ID] = true
	}
	return set
}

func mergeBoolSets(sets ...map[uint]bool) map[uint]bool {
	total := 0
	for _, set := range sets {
		total += len(set)
	}
	merged := make(map[uint]bool, total)
	for _, set := range sets {
		for id := range set {
			merged[id] = true
		}
	}
	return merged
}

// SaveAttendanceRecord 保存考勤记录到数据库
func (s *AttendanceRecordService) SaveAttendanceRecord(
	ctx context.Context,
	resp *dto.AttendanceDetailResponse,
) error {
	date, _ := time.ParseInLocation("2006-01-02", resp.Date, time.Local)

	// 序列化各类人员ID
	onTimeJSON, _ := s.serializeOnTimeUsers(resp.Users.OnTime)
	lateJSON, _ := s.serializeOnTimeUsers(resp.Users.Late)
	leaveJSON, _ := s.serializeLeaveUsers(resp.Users.Leave)
	notArrivedJSON, _ := s.serializeNotArrivedUsers(resp.Users.NotArrived)
	restDayJSON, _ := s.serializeRestDayUsers(resp.Users.RestDay)
	hasCourseJSON, _ := s.serializeBasicUsers(resp.Users.HasCourse)

	record := &model.AttendanceRecord{
		Date:          date,
		Week:          resp.Week,
		Section:       resp.Section,
		OnTimeIDs:     onTimeJSON,
		LateIDs:       lateJSON,
		LeaveIDs:      leaveJSON,
		NotArrivedIDs: notArrivedJSON,
		RestDayIDs:    restDayJSON,
		HasCourseIDs:  hasCourseJSON,
	}

	return s.attendanceRecordRepo.Upsert(ctx, record)
}

// GetAttendanceRecordFromDB 从数据库获取已保存的考勤记录
func (s *AttendanceRecordService) GetAttendanceRecordFromDB(
	ctx context.Context,
	req *dto.AttendanceDetailRequest,
) (*dto.AttendanceDetailResponse, error) {
	date, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
	if err != nil {
		return nil, response.ErrInvalidParamWithMsg("日期格式错误")
	}

	record, err := s.attendanceRecordRepo.FindByDateSection(ctx, date, req.Section)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.ErrNotFoundWithMsg("该时段暂无考勤记录")
		}
		return nil, err
	}

	// 获取涉及的所有用户信息
	userIDs := s.extractAllUserIDs(record)
	users, err := s.userRepo.ListByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	// 构建 userMap（按部门过滤）
	userMap, err := s.buildUserMapWithDeptFilter(ctx, users, req.DeptIDs)
	if err != nil {
		return nil, err
	}

	// 获取用户部门名称映射
	userDeptNames, err := s.userRepo.GetUserDepartmentNames(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	// 从数据库获取作息时间配置
	periods, err := s.getActivePeriods(ctx)
	if err != nil || req.Section > len(periods) {
		// 回退到配置文件
		periods = s.scheduleCfg.Periods
	}

	slotStart := periods[req.Section-1].Start
	slotEnd := periods[req.Section-1].End

	resp := dto.NewAttendanceDetailResponseFromRecord(record, slotStart, slotEnd, userMap, userDeptNames)
	resp, err = s.applyManualOverridesToDetail(ctx, date, req.Section, resp)
	if err != nil {
		return nil, err
	}
	slotStartAt, _, err := scheduleutil.CalculateSlotTime(date, req.Section, periods)
	if err == nil {
		resp.SetViewMetadata("final", true, s.calculateFinalizeAt(slotStartAt))
	}
	return resp, nil
}

func (s *AttendanceRecordService) FinalizeAttendanceRecord(
	ctx context.Context,
	req *dto.AttendanceDetailRequest,
) (*dto.AttendanceDetailResponse, error) {
	window, err := s.resolveAttendanceWindow(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, _, err := s.buildAttendanceDetail(ctx, req, window, minTime(window.finalizeAt, window.slotEnd), true)
	if err != nil {
		return nil, err
	}
	resp, err = s.applyManualOverridesToDetail(ctx, window.date, req.Section, resp)
	if err != nil {
		return nil, err
	}
	resp.SetViewMetadata("final", true, window.finalizeAt)

	if err := s.SaveAttendanceRecord(ctx, resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// getActivePeriods 获取当前生效的作息时间配置（内部辅助方法）
func (s *AttendanceRecordService) getActivePeriods(ctx context.Context) ([]config.Period, error) {
	if s.schedulePeriodSrv == nil {
		return s.scheduleCfg.Periods, nil
	}

	periods, err := s.schedulePeriodSrv.GetActivePeriods(ctx)
	if err != nil || len(periods) == 0 {
		return s.scheduleCfg.Periods, err
	}

	return periods, nil
}

// calculateCheckWindowStart 计算打卡窗口开始时间
// 第1节：当天00:00
// 第2节及以后：上一节的下课时间
func (s *AttendanceRecordService) calculateCheckWindowStart(
	ctx context.Context,
	date time.Time,
	section int,
) (time.Time, error) {
	if section <= 1 {
		// 第1节：从当天00:00开始
		return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location()), nil
	}

	// 第2节及以后：从上一节的下课时间开始
	periods := s.resolveActivePeriods(ctx)

	prevSection := section - 1
	if prevSection > len(periods) {
		return time.Time{}, response.NewBizError(response.CodeInvalidParam, "无效的节次")
	}

	// 获取上一节的下课时间
	prevPeriod := periods[prevSection-1]
	prevEndTime, err := time.Parse("15:04", prevPeriod.End)
	if err != nil {
		return time.Time{}, response.NewBizError(response.CodeInternalError, "解析上一节下课时间失败")
	}

	// 构造完整的日期时间
	windowStart := time.Date(
		date.Year(), date.Month(), date.Day(),
		prevEndTime.Hour(), prevEndTime.Minute(), 0, 0,
		date.Location(),
	)

	return windowStart, nil
}

func (s *AttendanceRecordService) resolveActivePeriods(ctx context.Context) []config.Period {
	if s.schedulePeriodSrv != nil {
		periods, err := s.schedulePeriodSrv.GetActivePeriods(ctx)
		if err == nil && len(periods) > 0 {
			return periods
		}
	}
	return s.scheduleCfg.Periods
}

func (s *AttendanceRecordService) loadAttendanceRecords(
	ctx context.Context,
	dingUserIDs []string,
	startAt, endAt time.Time,
) ([]dingtalk.CheckRecord, error) {
	if s.fetchAttendanceRecords != nil {
		return s.fetchAttendanceRecords(ctx, dingUserIDs, startAt, endAt)
	}
	if s.dingMgr == nil {
		return nil, response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}

	_, dingClient, err := s.dingMgr.FromContext(ctx)
	if err != nil {
		return nil, response.NewBizError(response.CodeUnauthorized, "缺少租户信息")
	}

	return dingClient.GetAttendanceRecords(ctx, dingUserIDs, startAt, endAt)
}

// getAttendanceCandidatesWithCourseUsers 获取考勤候选人与原始有课人员。
// deptIDs 为空时返回所有部门启用且用户状态启用的候选人，否则仅返回指定启用部门的候选人。
// 返回值：候选用户列表、有课用户列表。
func (s *AttendanceRecordService) getAttendanceCandidatesWithCourseUsers(
	ctx context.Context,
	date time.Time,
	week, section int,
	deptIDs []int64,
) ([]model.User, []model.User, error) {
	// 1. 获取候选用户（用户启用且至少归属一个启用部门）
	activeUsers, err := s.userRepo.ListAttendanceCandidates(ctx, deptIDs)
	if err != nil {
		return nil, nil, errs.WrapMsgErr("获取候选用户失败", err)
	}

	if len(activeUsers) == 0 {
		return []model.User{}, []model.User{}, nil
	}

	// 2. 判断是否使用"全体应到"模式（假期模式或超出学期时间）
	if s.shouldUseAllAttendMode(ctx, date) {
		// 全体应到模式：不排除有课人员
		return activeUsers, []model.User{}, nil
	}

	// 3. 正常模式：获取该时段有课的人员
	dayOfWeek := scheduleutil.WeekdayMon1Sun7(date)
	userIDs := make([]uint, 0, len(activeUsers))
	for _, u := range activeUsers {
		userIDs = append(userIDs, u.ID)
	}

	courses, err := s.courseRepo.ListByUsersDaySection(ctx, userIDs, dayOfWeek, section)
	if err != nil {
		return nil, nil, errs.WrapMsgErr("获取用户课表失败", err)
	}

	// 4. 过滤出本周有课的用户
	busyUserSet := make(map[uint]bool)
	for _, c := range courses {
		if weekutil.ContainsWeek(c.WeekList, week) {
			busyUserSet[c.UserID] = true
		}
	}

	hasCourse := make([]model.User, 0, len(busyUserSet))
	for _, u := range activeUsers {
		if busyUserSet[u.ID] {
			hasCourse = append(hasCourse, u)
		}
	}

	return activeUsers, hasCourse, nil
}

// getShouldAttendUsers 获取应到人员（候选人 - 有课人员）
// deptIDs 为空时返回所有部门启用且用户状态启用的候选人，否则仅返回指定启用部门的候选人
// 返回值：应到用户列表、有课用户列表
func (s *AttendanceRecordService) getShouldAttendUsers(
	ctx context.Context,
	date time.Time,
	week, section int,
	deptIDs []int64,
) ([]model.User, []model.User, error) {
	activeUsers, hasCourse, err := s.getAttendanceCandidatesWithCourseUsers(ctx, date, week, section, deptIDs)
	if err != nil {
		return nil, nil, err
	}

	// 5. 应到人员 = 候选人 - 有课人员
	shouldAttend := filterModelUsers(activeUsers, userIDBoolSet(hasCourse))
	return shouldAttend, hasCourse, nil
}

// getOnTimeUsers 获取正常打卡人员（在有效时间窗口内打卡的人）
func (s *AttendanceRecordService) getOnTimeUsers(
	ctx context.Context,
	users []model.User,
	date time.Time,
	section int, // 节次（用于计算打卡窗口）
	deadline time.Time, // 上课时间（打卡截止时间）
	queryEnd time.Time,
) ([]dto.AttendanceUserCheck, []dto.AttendanceUserCheck, error) {
	if len(users) == 0 {
		return []dto.AttendanceUserCheck{}, []dto.AttendanceUserCheck{}, nil
	}

	// 提取钉钉用户ID
	dingUserIDs := make([]string, 0, len(users))
	userByDingID := make(map[string]*model.User)
	for i := range users {
		if users[i].DingUserID != "" {
			dingUserIDs = append(dingUserIDs, users[i].DingUserID)
			userByDingID[users[i].DingUserID] = &users[i]
		}
	}

	if len(dingUserIDs) == 0 {
		return []dto.AttendanceUserCheck{}, []dto.AttendanceUserCheck{}, nil
	}

	// 【核心修改】计算有效打卡窗口
	windowStart, err := s.calculateCheckWindowStart(ctx, date, section)
	if err != nil {
		return nil, nil, errs.WrapMsgErr("计算打卡窗口失败", err)
	}

	// 查询打卡记录：从窗口开始到下课时间
	// 注意：仍然查询到下课时间，以便统计迟到人数
	queryStart := windowStart
	records, err := s.loadAttendanceRecords(ctx, dingUserIDs, queryStart, queryEnd)
	if err != nil {
		s.logger.Errorw("获取钉钉打卡记录失败",
			"userCount", len(dingUserIDs),
			"queryStart", queryStart,
			"queryEnd", queryEnd,
			"error", err,
		)
		return nil, nil, errs.WrapMsgErr("获取钉钉打卡记录失败", err)
	}

	// 按用户ID去重，取最早的打卡记录
	earliestCheck := make(map[string]time.Time)
	for _, r := range records {
		// 只统计上班打卡
		if r.CheckType != "OnDuty" {
			continue
		}
		if existing, ok := earliestCheck[r.DingUserID]; !ok || r.CheckTime.Before(existing) {
			earliestCheck[r.DingUserID] = r.CheckTime
		}
	}

	// 【核心修改】只返回在有效窗口内打卡的人
	onTime := make([]dto.AttendanceUserCheck, 0)
	lateUsers := make([]dto.AttendanceUserCheck, 0)

	for dingUserID, checkTime := range earliestCheck {
		user := userByDingID[dingUserID]
		if user == nil {
			s.logger.Warnw("找不到对应的用户", "dingUserID", dingUserID)
			continue
		}

		// 【新增】检查打卡时间是否在有效窗口内
		if checkTime.Before(windowStart) {
			continue // 跳过这条打卡记录
		}

		// 判断是否迟到
		if checkTime.Before(deadline) || checkTime.Equal(deadline) {
			onTime = append(onTime, dto.AttendanceUserCheck{
				ID:        user.ID,
				Name:      user.Name,
				CheckTime: checkTime,
			})
		} else {
			lateUsers = append(lateUsers, dto.AttendanceUserCheck{
				ID:        user.ID,
				Name:      user.Name,
				CheckTime: checkTime,
			})
		}
	}

	return onTime, lateUsers, nil
}

func (s *AttendanceRecordService) SendLateNotifications(
	ctx context.Context,
	date string,
	section int,
	slotStart string,
	slotEnd string,
	mode string,
	lateUsers []model.User,
) error {
	if len(lateUsers) == 0 {
		return nil
	}
	if s.scheduleSettingRepo != nil {
		enabled, _ := s.scheduleSettingRepo.IsLateNotifyEnabled(ctx)
		if !enabled {
			return nil
		}
	}
	if s.dingMgr == nil {
		return response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}
	tenant, dingClient, err := s.dingMgr.FromContext(ctx)
	if err != nil {
		return response.NewBizError(response.CodeUnauthorized, "缺少租户信息")
	}

	if tenant.AgentID == "" {
		return response.NewBizError(response.CodeInvalidParam, "钉钉AgentID未配置")
	}

	dingUserIDs := make([]string, 0, len(lateUsers))
	lateUserNames := make([]string, 0, len(lateUsers))
	for i := range lateUsers {
		if lateUsers[i].DingUserID != "" {
			dingUserIDs = append(dingUserIDs, lateUsers[i].DingUserID)
			lateUserNames = append(lateUserNames, lateUsers[i].Name)
		}
	}

	if len(dingUserIDs) == 0 {
		return nil
	}

	periodLabel := fmt.Sprintf("第%d节", section)
	if mode == model.ScheduleModeHoliday {
		switch section {
		case 1:
			periodLabel = "上午"
		case 2:
			periodLabel = "下午"
		case 3:
			periodLabel = "晚上"
		default:
			periodLabel = fmt.Sprintf("第%d次", section)
		}
	}
	content := fmt.Sprintf("你在%s %s考勤(%s-%s)未正常打卡，请及时补签或联系管理员。", date, periodLabel, slotStart, slotEnd)
	if err := dingClient.SendWorkNoticeText(ctx, tenant.AgentID, dingUserIDs, content); err != nil {
		return errs.WrapMsgErr("发送钉钉迟到提醒失败", err)
	}

	return nil
}

// getLeaveUsers 获取请假人员
func (s *AttendanceRecordService) getLeaveUsers(
	ctx context.Context,
	users []model.User,
	slotStart, slotEnd time.Time,
) ([]dto.AttendanceUserLeave, error) {
	if len(users) == 0 {
		return []dto.AttendanceUserLeave{}, nil
	}

	// 提取用户ID
	userIDs := make([]uint, 0, len(users))
	userMap := make(map[uint]*model.User)
	for i := range users {
		userIDs = append(userIDs, users[i].ID)
		userMap[users[i].ID] = &users[i]
	}

	// 查询已通过的请假记录
	leaveRecords, err := s.leaveRepo.ListApprovedByUserIDs(ctx, userIDs, slotStart, slotEnd)
	if err != nil {
		return nil, errs.WrapMsgErr("获取请假记录失败", err)
	}

	// 按用户ID去重
	seen := make(map[uint]bool)
	result := make([]dto.AttendanceUserLeave, 0)
	for _, leave := range leaveRecords {
		if seen[leave.UserID] {
			continue
		}
		seen[leave.UserID] = true

		user := userMap[leave.UserID]
		if user == nil {
			continue
		}

		result = append(result, dto.AttendanceUserLeave{
			ID:        user.ID,
			Name:      user.Name,
			LeaveType: leave.LeaveType,
			Reason:    leave.Reason,
		})
	}

	return result, nil
}

// calculateNotArrived 计算未到人员（应到 - 正常打卡 - 请假）
func (s *AttendanceRecordService) calculateNotArrived(
	shouldAttend []model.User,
	onTime []dto.AttendanceUserCheck,
	leave []dto.AttendanceUserLeave,
) []dto.AttendanceUserBasic {
	return s.calculateNotArrivedWithLate(shouldAttend, onTime, nil, leave)
}

func (s *AttendanceRecordService) calculateNotArrivedWithLate(
	shouldAttend []model.User,
	onTime []dto.AttendanceUserCheck,
	late []dto.AttendanceUserCheck,
	leave []dto.AttendanceUserLeave,
) []dto.AttendanceUserBasic {
	// 构建已处理用户集合
	processed := make(map[uint]bool)
	for _, u := range onTime {
		processed[u.ID] = true
	}
	for _, u := range late {
		processed[u.ID] = true
	}
	for _, u := range leave {
		processed[u.ID] = true
	}

	// 未到 = 应到 - 正常打卡 - 请假
	notArrived := make([]dto.AttendanceUserBasic, 0)
	for _, u := range shouldAttend {
		if !processed[u.ID] {
			notArrived = append(notArrived, dto.AttendanceUserBasic{
				ID:   u.ID,
				Name: u.Name,
			})
		}
	}

	return notArrived
}

func filterAttendanceChecks(users []dto.AttendanceUserCheck, excluded map[uint]bool) []dto.AttendanceUserCheck {
	if len(excluded) == 0 {
		return users
	}
	filtered := make([]dto.AttendanceUserCheck, 0, len(users))
	for _, user := range users {
		if !excluded[user.ID] {
			filtered = append(filtered, user)
		}
	}
	return filtered
}

// buildNotifyList 返回需要发送通知的人员：应到但未正常打卡且未请假（含迟到和缺勤）
// 逻辑与 calculateNotArrived 相同，但保留完整 model.User（含 DingUserID）
func buildNotifyList(shouldAttend []model.User, onTime []dto.AttendanceUserCheck, leave []dto.AttendanceUserLeave) []model.User {
	processed := make(map[uint]bool, len(onTime)+len(leave))
	for _, u := range onTime {
		processed[u.ID] = true
	}
	for _, u := range leave {
		processed[u.ID] = true
	}
	result := make([]model.User, 0)
	for _, u := range shouldAttend {
		if !processed[u.ID] {
			result = append(result, u)
		}
	}
	return result
}

// 序列化方法
func (s *AttendanceRecordService) serializeOnTimeUsers(users []dto.AttendanceUserCheck) (string, error) {
	data := make([]dto.StoredUserCheck, 0, len(users))
	for _, u := range users {
		data = append(data, dto.StoredUserCheck{
			ID:        u.ID,
			CheckTime: u.CheckTime.Unix(),
		})
	}
	b, err := json.Marshal(data)
	return string(b), err
}

func (s *AttendanceRecordService) serializeLeaveUsers(users []dto.AttendanceUserLeave) (string, error) {
	data := make([]dto.StoredUserLeave, 0, len(users))
	for _, u := range users {
		data = append(data, dto.StoredUserLeave{
			ID:        u.ID,
			LeaveType: u.LeaveType,
			Reason:    u.Reason,
		})
	}
	b, err := json.Marshal(data)
	return string(b), err
}

func (s *AttendanceRecordService) serializeNotArrivedUsers(users []dto.AttendanceUserBasic) (string, error) {
	ids := make([]uint, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	b, err := json.Marshal(ids)
	return string(b), err
}

func (s *AttendanceRecordService) serializeRestDayUsers(users []dto.AttendanceUserBasic) (string, error) {
	ids := make([]uint, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	b, err := json.Marshal(ids)
	return string(b), err
}

func (s *AttendanceRecordService) serializeBasicUsers(users []dto.AttendanceUserBasic) (string, error) {
	ids := make([]uint, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	b, err := json.Marshal(ids)
	return string(b), err
}

// toBasicList 将 model.User 列表转为 AttendanceUserBasic 列表（不含部门名）
func toBasicList(users []model.User) []dto.AttendanceUserBasic {
	list := make([]dto.AttendanceUserBasic, 0, len(users))
	for _, u := range users {
		list = append(list, dto.AttendanceUserBasic{
			ID:     u.ID,
			Name:   u.Name,
			Avatar: u.Avatar,
		})
	}
	return list
}

// filterRestDayUsers 从应到人员中筛出今日休息日的用户
func (s *AttendanceRecordService) filterRestDayUsers(
	ctx context.Context,
	users []model.User,
	dayOfWeek int,
) []model.User {
	if len(users) == 0 || s.restDayRepo == nil {
		return nil
	}

	userIDs := make([]uint, 0, len(users))
	for _, u := range users {
		userIDs = append(userIDs, u.ID)
	}

	restDays, err := s.restDayRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		s.logger.Warnw("查询休息日记录失败", "error", err)
		return nil
	}

	restDayUserSet := make(map[uint]struct{})
	for _, rd := range restDays {
		if *rd.DayOfWeek == dayOfWeek {
			restDayUserSet[rd.UserID] = struct{}{}
		}
	}

	if len(restDayUserSet) == 0 {
		return nil
	}

	result := make([]model.User, 0, len(restDayUserSet))
	for _, u := range users {
		if _, ok := restDayUserSet[u.ID]; ok {
			result = append(result, u)
		}
	}
	return result
}

// toRestDayBasicList 将 model.User 列表转为 AttendanceUserBasic 列表
func toRestDayBasicList(users []model.User) []dto.AttendanceUserBasic {
	if len(users) == 0 {
		return []dto.AttendanceUserBasic{}
	}
	result := make([]dto.AttendanceUserBasic, 0, len(users))
	for _, u := range users {
		result = append(result, dto.AttendanceUserBasic{
			ID:   u.ID,
			Name: u.Name,
		})
	}
	return result
}

// GetAttendanceRecordsByDate 获取某天所有节次的考勤记录
func (s *AttendanceRecordService) GetAttendanceRecordsByDate(
	ctx context.Context,
	date time.Time,
	deptIDs []int64,
) ([]*dto.AttendanceDetailResponse, error) {
	// 1. 从数据库获取该日期的所有考勤记录
	records, err := s.attendanceRecordRepo.ListByDate(ctx, date)
	if err != nil {
		return nil, errs.WrapMsgErr("获取考勤记录失败", err)
	}

	if len(records) == 0 {
		return nil, response.ErrNotFoundWithMsg("该日期暂无考勤记录")
	}

	// 2. 收集所有记录中涉及的用户ID
	allUserIDs := make(map[uint]bool)
	for i := range records {
		ids := s.extractAllUserIDs(&records[i])
		for _, id := range ids {
			allUserIDs[id] = true
		}
	}

	userIDList := make([]uint, 0, len(allUserIDs))
	for id := range allUserIDs {
		userIDList = append(userIDList, id)
	}

	// 3. 获取用户信息
	users, err := s.userRepo.ListByIDs(ctx, userIDList)
	if err != nil {
		return nil, errs.WrapMsgErr("获取用户信息失败", err)
	}

	// 4. 构建 userMap（按部门过滤）
	userMap, err := s.buildUserMapWithDeptFilter(ctx, users, deptIDs)
	if err != nil {
		return nil, err
	}

	// 5. 获取用户部门名称映射
	userDeptNames, err := s.userRepo.GetUserDepartmentNames(ctx, userIDList)
	if err != nil {
		return nil, err
	}

	// 6. 获取作息时间配置
	periods, err := s.getActivePeriods(ctx)
	if err != nil {
		periods = s.scheduleCfg.Periods
	}

	// 7. 构建响应列表
	results := make([]*dto.AttendanceDetailResponse, 0, len(records))
	for i := range records {
		record := &records[i]
		if record.Section <= 0 || record.Section > len(periods) {
			continue
		}
		slotStart := periods[record.Section-1].Start
		slotEnd := periods[record.Section-1].End
		resp := dto.NewAttendanceDetailResponseFromRecord(record, slotStart, slotEnd, userMap, userDeptNames)
		results = append(results, resp)
	}

	return results, nil
}

func (s *AttendanceRecordService) extractAllUserIDs(record *model.AttendanceRecord) []uint {
	idSet := make(map[uint]bool)

	var onTime []dto.StoredUserCheck
	json.Unmarshal([]byte(record.OnTimeIDs), &onTime)
	for _, u := range onTime {
		idSet[u.ID] = true
	}

	var late []dto.StoredUserCheck
	json.Unmarshal([]byte(record.LateIDs), &late)
	for _, u := range late {
		idSet[u.ID] = true
	}

	var leave []dto.StoredUserLeave
	json.Unmarshal([]byte(record.LeaveIDs), &leave)
	for _, u := range leave {
		idSet[u.ID] = true
	}

	var notArrived []uint
	json.Unmarshal([]byte(record.NotArrivedIDs), &notArrived)
	for _, id := range notArrived {
		idSet[id] = true
	}

	var restDay []uint
	if err := json.Unmarshal([]byte(record.RestDayIDs), &restDay); err != nil && record.RestDayIDs != "" && record.RestDayIDs != "null" {
		s.logger.Warnw("解析RestDayIDs失败", "recordID", record.ID, "error", err)
	}
	for _, id := range restDay {
		idSet[id] = true
	}

	var hasCourse []uint
	if record.HasCourseIDs != "" && record.HasCourseIDs != "null" {
		if err := json.Unmarshal([]byte(record.HasCourseIDs), &hasCourse); err != nil {
			s.logger.Warnw("解析HasCourseIDs失败", "recordID", record.ID, "error", err)
		}
	}
	for _, id := range hasCourse {
		idSet[id] = true
	}

	ids := make([]uint, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	return ids
}

// GetWeeklyRanking 获取本周考勤迟到排行（迟到次数从大到小）
func (s *AttendanceRecordService) GetWeeklyRanking(ctx context.Context, req *dto.WeeklyAttendanceRankingRequest) (*dto.WeeklyAttendanceRankingResponse, error) {
	// 1. 计算周起止时间（周一到周日）
	now := time.Now()
	// Weekday(): Sunday=0, Monday=1...
	offset := int(time.Monday - now.Weekday())
	if offset > 0 {
		offset = -6 // 如果今天是周日(0)，则周一是6天前
	}
	// 应用偏移量：0=本周，1=上周，2=上上周
	totalOffset := offset - (req.WeekOffset * 7)

	// 周一 00:00:00
	weekStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, totalOffset)
	// 周日 00:00:00
	weekEnd := weekStart.AddDate(0, 0, 6)

	// 2. 查询指定周所有考勤记录
	records, err := s.attendanceRecordRepo.ListByDateRange(ctx, weekStart, weekEnd)
	if err != nil {
		return nil, errs.WrapMsgErr("获取考勤记录失败", err)
	}

	// 3. 统计迟到/未到次数
	lateCounts := make(map[uint]int)
	for _, r := range records {
		if r.LateIDs == "" || r.LateIDs == "[]" {
			continue
		}
		var lateUsers []dto.StoredUserCheck
		if err := json.Unmarshal([]byte(r.LateIDs), &lateUsers); err != nil {
			s.logger.Warnw("反序列化迟到人员失败", "id", r.ID, "error", err)
			continue
		}
		for _, user := range lateUsers {
			lateCounts[user.ID]++
		}
	}

	if len(lateCounts) == 0 {
		return &dto.WeeklyAttendanceRankingResponse{Items: []dto.AttendanceRankingItem{}}, nil
	}

	// 4. 获取用户信息（支持部门过滤）
	var users []model.User

	if len(req.DeptIDs) > 0 {
		// 构造迟到用户ID集合，结合部门进行交集过滤
		onlyIDs := make([]uint, 0, len(lateCounts))
		for uid := range lateCounts {
			onlyIDs = append(onlyIDs, uid)
		}
		var err error
		users, err = s.userRepo.ListByScope(ctx, req.DeptIDs, onlyIDs)
		if err != nil {
			return nil, errs.WrapMsgErr("获取部门用户失败", err)
		}
	} else {
		// 未指定部门，获取所有迟到用户
		userIDs := make([]uint, 0, len(lateCounts))
		for uid := range lateCounts {
			userIDs = append(userIDs, uid)
		}
		users, err = s.userRepo.ListByIDs(ctx, userIDs)
		if err != nil {
			return nil, errs.WrapMsgErr("获取用户信息失败", err)
		}
	}

	// 5. 构建结果项
	items := make([]dto.AttendanceRankingItem, 0, len(users))
	for _, u := range users {
		count := lateCounts[u.ID]
		items = append(items, dto.AttendanceRankingItem{
			UserID:    u.ID,
			Name:      u.Name,
			Avatar:    u.Avatar,
			LateCount: count,
		})
	}

	// 6. 排序：迟到次数从大到小
	sort.Slice(items, func(i, j int) bool {
		return items[i].LateCount > items[j].LateCount
	})

	return &dto.WeeklyAttendanceRankingResponse{Items: items}, nil
}

// GetWeeklyAttendanceRateRanking 获取本周出勤率排行（出勤率从高到低，取 Top 10）
func (s *AttendanceRecordService) GetWeeklyAttendanceRateRanking(
	ctx context.Context,
	req *dto.WeeklyAttendanceRankingRequest,
) (*dto.WeeklyAttendanceRateRankingResponse, error) {
	// 1. 计算周起止时间（复用 GetWeeklyRanking 的逻辑）
	now := time.Now()
	offset := int(time.Monday - now.Weekday())
	if offset > 0 {
		offset = -6
	}
	totalOffset := offset - (req.WeekOffset * 7)
	weekStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, totalOffset)
	weekEnd := weekStart.AddDate(0, 0, 6)

	// 2. 查询指定周所有考勤记录
	records, err := s.attendanceRecordRepo.ListByDateRange(ctx, weekStart, weekEnd)
	if err != nil {
		return nil, errs.WrapMsgErr("获取考勤记录失败", err)
	}

	// 3. 统计每个用户的应到次数和正常签到次数
	totalSlots := make(map[uint]int)
	onTimeSlots := make(map[uint]int)

	for _, r := range records {
		// 解析正常签到人员
		var onTime []dto.StoredUserCheck
		if r.OnTimeIDs != "" && r.OnTimeIDs != "[]" {
			json.Unmarshal([]byte(r.OnTimeIDs), &onTime)
		}
		onTimeSet := make(map[uint]bool, len(onTime))
		for _, u := range onTime {
			onTimeSet[u.ID] = true
		}

		// 解析未到人员
		var notArrived []uint
		if r.NotArrivedIDs != "" && r.NotArrivedIDs != "[]" {
			json.Unmarshal([]byte(r.NotArrivedIDs), &notArrived)
		}

		// 解析迟到人员
		var lateUsers []dto.StoredUserCheck
		if r.LateIDs != "" && r.LateIDs != "[]" {
			json.Unmarshal([]byte(r.LateIDs), &lateUsers)
		}

		// 解析请假人员
		var leaveUsers []dto.StoredUserLeave
		if r.LeaveIDs != "" && r.LeaveIDs != "[]" {
			json.Unmarshal([]byte(r.LeaveIDs), &leaveUsers)
		}

		// 应到用户 = onTime + notArrived + leave（不含 hasCourse）
		for _, u := range onTime {
			totalSlots[u.ID]++
			onTimeSlots[u.ID]++
		}
		for _, uid := range notArrived {
			totalSlots[uid]++
		}
		for _, u := range lateUsers {
			totalSlots[u.ID]++
		}
		for _, u := range leaveUsers {
			totalSlots[u.ID]++
		}
	}

	if len(totalSlots) == 0 {
		return &dto.WeeklyAttendanceRateRankingResponse{Items: []dto.AttendanceRateRankingItem{}}, nil
	}

	// 4. 计算出勤率并排序
	type rateEntry struct {
		userID uint
		onTime int
		total  int
		rate   float64
	}
	entries := make([]rateEntry, 0, len(totalSlots))
	for uid, total := range totalSlots {
		if total == 0 {
			continue
		}
		onTime := onTimeSlots[uid]
		rate := float64(onTime) / float64(total) * 100
		entries = append(entries, rateEntry{userID: uid, onTime: onTime, total: total, rate: rate})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rate > entries[j].rate
	})
	if len(entries) > 10 {
		entries = entries[:10]
	}

	// 5. 获取用户信息
	userIDs := make([]uint, len(entries))
	for i, e := range entries {
		userIDs[i] = e.userID
	}
	users, err := s.userRepo.ListByIDs(ctx, userIDs)
	if err != nil {
		return nil, errs.WrapMsgErr("获取用户信息失败", err)
	}
	userMap := make(map[uint]*model.User, len(users))
	for i := range users {
		userMap[users[i].ID] = &users[i]
	}

	// 6. 构建响应
	items := make([]dto.AttendanceRateRankingItem, 0, len(entries))
	for _, e := range entries {
		u := userMap[e.userID]
		if u == nil {
			continue
		}
		items = append(items, dto.AttendanceRateRankingItem{
			UserID:      u.ID,
			Name:        u.Name,
			Avatar:      u.Avatar,
			OnTimeCount: e.onTime,
			TotalCount:  e.total,
			Rate:        fmt.Sprintf("%.0f%%", e.rate),
		})
	}

	return &dto.WeeklyAttendanceRateRankingResponse{Items: items}, nil
}

// buildUserMapWithDeptFilter 构建用户映射（支持部门过滤）
// - deptIDs 为空：返回所有用户的映射
// - deptIDs 非空：只返回属于指定部门且满足考勤候选人口径的用户
func (s *AttendanceRecordService) buildUserMapWithDeptFilter(
	ctx context.Context,
	users []model.User,
	deptIDs []int64,
) (map[uint]*model.User, error) {
	userMap := make(map[uint]*model.User)

	if len(deptIDs) == 0 {
		for i := range users {
			userMap[users[i].ID] = &users[i]
		}
		return userMap, nil
	}

	// 快照筛选需要和实时详情保持同一口径：
	// 只保留“用户启用且命中启用部门”的考勤候选人，而不是简单的部门成员关系。
	deptUsers, err := s.userRepo.ListAttendanceCandidates(ctx, deptIDs)
	if err != nil {
		return nil, errs.WrapMsgErr("获取部门考勤候选人失败", err)
	}

	deptUserSet := make(map[uint]bool, len(deptUsers))
	for _, u := range deptUsers {
		deptUserSet[u.ID] = true
	}

	// 只保留属于指定部门的用户
	for i := range users {
		if deptUserSet[users[i].ID] {
			userMap[users[i].ID] = &users[i]
		}
	}

	return userMap, nil
}

// GetAttendanceText 获取考勤文本（用于复制到群里）
// 从数据库获取已保存的考勤记录，生成简洁的文本格式
func (s *AttendanceRecordService) GetAttendanceText(
	ctx context.Context,
	req *dto.AttendanceTextRequest,
) (*dto.AttendanceTextResponse, error) {
	// 1. 按实时/快照规则获取考勤数据
	detailReq := &dto.AttendanceDetailRequest{
		Date:    req.Date,
		Week:    req.Week,
		Section: req.Section,
		DeptIDs: req.DeptIDs,
	}

	detail, err := s.GetAttendanceDetail(ctx, detailReq)
	if err != nil {
		return nil, err
	}

	// 2. 获取当前模式（假期模式或上学模式）
	mode, err := s.schedulePeriodSrv.GetCurrentMode(ctx)
	if err != nil {
		// 如果获取失败，记录警告并默认使用上学模式
		s.logger.Warnw("获取作息模式失败，使用默认上学模式", "error", err)
		mode = model.ScheduleModeSchool
	}

	// 3. 获取当前生效的作息时间配置（用于获取节次名称）
	periods := s.resolveActivePeriods(ctx)

	// 4. 生成文本格式
	return s.formatAttendanceText(detail, mode, periods), nil
}

// FormatAttendanceTextFromDetail 直接从已计算的考勤结果生成推送文本
// 相比 GetAttendanceText，不重新读取数据库，避免因保存失败而推送旧数据
func (s *AttendanceRecordService) FormatAttendanceTextFromDetail(
	ctx context.Context,
	result *dto.AttendanceDetailResponse,
	deptIDs []int64,
) (*dto.AttendanceTextResponse, error) {
	detail := result

	// 如果有部门过滤，在内存中过滤用户列表
	if len(deptIDs) > 0 {
		deptUsers, err := s.userRepo.ListByScope(ctx, deptIDs, nil)
		if err != nil {
			return nil, errs.WrapMsgErr("获取部门用户失败", err)
		}
		deptSet := make(map[uint]bool, len(deptUsers))
		for _, u := range deptUsers {
			deptSet[u.ID] = true
		}
		detail = filterDetailByDeptSet(result, deptSet)
	}

	mode, err := s.schedulePeriodSrv.GetCurrentMode(ctx)
	if err != nil {
		s.logger.Warnw("获取作息模式失败，使用默认上学模式", "error", err)
		mode = model.ScheduleModeSchool
	}

	periods := s.resolveActivePeriods(ctx)
	return s.formatAttendanceText(detail, mode, periods), nil
}

// filterDetailByDeptSet 按部门用户集合在内存中过滤考勤详情
func filterDetailByDeptSet(result *dto.AttendanceDetailResponse, deptSet map[uint]bool) *dto.AttendanceDetailResponse {
	onTime := make([]dto.AttendanceUserCheck, 0)
	for _, u := range result.Users.OnTime {
		if deptSet[u.ID] {
			onTime = append(onTime, u)
		}
	}
	late := make([]dto.AttendanceUserCheck, 0)
	for _, u := range result.Users.Late {
		if deptSet[u.ID] {
			late = append(late, u)
		}
	}
	leave := make([]dto.AttendanceUserLeave, 0)
	for _, u := range result.Users.Leave {
		if deptSet[u.ID] {
			leave = append(leave, u)
		}
	}
	notArrived := make([]dto.AttendanceUserBasic, 0)
	for _, u := range result.Users.NotArrived {
		if deptSet[u.ID] {
			notArrived = append(notArrived, u)
		}
	}
	restDay := make([]dto.AttendanceUserBasic, 0)
	for _, u := range result.Users.RestDay {
		if deptSet[u.ID] {
			restDay = append(restDay, u)
		}
	}
	shouldAttend := make([]dto.AttendanceUserBasic, 0)
	for _, u := range result.Users.ShouldAttend {
		if deptSet[u.ID] {
			shouldAttend = append(shouldAttend, u)
		}
	}
	return &dto.AttendanceDetailResponse{
		RecordID:    result.RecordID,
		Date:        result.Date,
		Week:        result.Week,
		Section:     result.Section,
		ViewMode:    result.ViewMode,
		IsFinalized: result.IsFinalized,
		FinalizeAt:  result.FinalizeAt,
		SlotTime:    result.SlotTime,
		Statistics: dto.AttendanceStatistics{
			ShouldAttend: len(shouldAttend),
			OnTime:       len(onTime),
			Late:         len(late),
			Leave:        len(leave),
			NotArrived:   len(notArrived),
			RestDay:      len(restDay),
			HasCourse:    len(result.Users.HasCourse),
		},
		Users: dto.AttendanceUserLists{
			ShouldAttend: shouldAttend,
			OnTime:       onTime,
			Late:         late,
			Leave:        leave,
			NotArrived:   notArrived,
			RestDay:      restDay,
			HasCourse:    result.Users.HasCourse,
		},
	}
}

func notArrivedLabel(detail *dto.AttendanceDetailResponse) string {
	if detail == nil {
		return "未到"
	}
	if detail.ViewMode == "current" {
		return "当前未到"
	}
	if detail.ViewMode == "final" {
		return "未到"
	}
	return "未到"
}

// formatAttendanceText 将考勤详情格式化为文本
func (s *AttendanceRecordService) formatAttendanceText(detail *dto.AttendanceDetailResponse, mode string, periods []config.Period) *dto.AttendanceTextResponse {
	// 解析日期以获取星期几
	date, _ := time.ParseInLocation("2006-01-02", detail.Date, time.Local)
	weekdayNum := scheduleutil.WeekdayMon1Sun7(date)
	weekdayStr := getWeekdayName(weekdayNum)

	// 根据模式生成不同的节次标签
	var periodLabel string
	if mode == model.ScheduleModeHoliday {
		// 假期模式：使用上午/下午/晚上
		switch detail.Section {
		case 1:
			periodLabel = "上午"
		case 2:
			periodLabel = "下午"
		case 3:
			periodLabel = "晚上"
		default:
			periodLabel = "第" + intToString(detail.Section) + "次"
		}
	} else {
		// 上学模式：使用 schedule_periods 中的 name 字段
		if detail.Section > 0 && detail.Section <= len(periods) {
			periodLabel = periods[detail.Section-1].Name
		} else {
			// 回退到默认格式
			periodLabel = "第" + intToString(detail.Section) + "节"
		}
	}

	// 构建标题：根据模式决定是否显示周次
	var title string
	if mode == model.ScheduleModeHoliday {
		// 假期模式：日期 + 星期 + 时段（不显示周次）
		title = "📅 " + detail.Date + " " + weekdayStr + " " + periodLabel + " 考勤"
	} else {
		// 上学模式：日期 + 星期 + 第X周 + 节次
		title = "📅 " + detail.Date + " " + weekdayStr + " 第" + intToString(detail.Week) + "周 " + periodLabel + " 考勤"
	}

	// 构建统计信息
	statistics := "⬇️应到" + intToString(detail.Statistics.ShouldAttend) + "人，" +
		"准时打卡" + intToString(detail.Statistics.OnTime) + "人，" +
		"请假" + intToString(detail.Statistics.Leave) + "人"
	if detail.ViewMode == "" {
		statistics += "，未到" + intToString(detail.Statistics.NotArrived) + "人"
	} else {
		statistics += "，迟到" + intToString(detail.Statistics.Late) + "人，" +
			notArrivedLabel(detail) + intToString(detail.Statistics.NotArrived) + "人"
	}
	if detail.Statistics.RestDay > 0 {
		statistics += "，休息" + intToString(detail.Statistics.RestDay) + "人"
	}

	// 构建分类人员列表
	content := make([]string, 0, 3)

	// 准时到
	if len(detail.Users.OnTime) > 0 {
		names := make([]string, 0, len(detail.Users.OnTime))
		for _, u := range detail.Users.OnTime {
			names = append(names, u.Name)
		}
		line := "🌟准时到(" + intToString(len(detail.Users.OnTime)) + "人)：" + joinNames(names)
		content = append(content, line)
	}

	// 请假
	if len(detail.Users.Leave) > 0 {
		names := make([]string, 0, len(detail.Users.Leave))
		for _, u := range detail.Users.Leave {
			names = append(names, u.Name)
		}
		line := "⏳请假(" + intToString(len(detail.Users.Leave)) + "人)：" + joinNames(names)
		content = append(content, line)
	}

	// 休息
	if len(detail.Users.RestDay) > 0 {
		names := make([]string, 0, len(detail.Users.RestDay))
		for _, u := range detail.Users.RestDay {
			names = append(names, u.Name)
		}
		line := "😴休息日(" + intToString(len(detail.Users.RestDay)) + "人)：" + joinNames(names)
		content = append(content, line)
	}

	if len(detail.Users.Late) > 0 {
		names := make([]string, 0, len(detail.Users.Late))
		for _, u := range detail.Users.Late {
			names = append(names, u.Name)
		}
		line := "❗迟到(" + intToString(len(detail.Users.Late)) + "人)：" + joinNames(names)
		content = append(content, line)
	}

	// 迟到/未到
	if len(detail.Users.NotArrived) > 0 {
		names := make([]string, 0, len(detail.Users.NotArrived))
		for _, u := range detail.Users.NotArrived {
			names = append(names, u.Name)
		}
		line := "❗️️迟到(" + intToString(len(detail.Users.NotArrived)) + "人)：" + joinNames(names)
		if detail.ViewMode != "" {
			line = "⏳" + notArrivedLabel(detail) + "(" + intToString(len(detail.Users.NotArrived)) + "人)：" + joinNames(names)
		}
		content = append(content, line)
	}

	// 构建完整文本
	fullText := title + "\n" + statistics
	for _, line := range content {
		fullText += "\n" + line
	}

	return &dto.AttendanceTextResponse{
		Title:      title,
		Statistics: statistics,
		Content:    content,
		FullText:   fullText,
	}
}

// joinNames 将姓名数组用顿号连接
func joinNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	result := ""
	for i, name := range names {
		if i > 0 {
			result += "、"
		}
		result += name
	}
	return result
}

// intToString 将整数转为字符串（简单实现）
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + intToString(-n)
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// getWeekdayName 将星期数字转换为中文名称
// weekday: 1=周一, 2=周二, ..., 7=周日
func getWeekdayName(weekday int) string {
	weekdays := []string{"", "周一", "周二", "周三", "周四", "周五", "周六", "周日"}
	if weekday >= 1 && weekday <= 7 {
		return weekdays[weekday]
	}
	return "周?"
}

// SignForUsers 代签（支持实时 date+section 和历史 record_id）
func (s *AttendanceRecordService) SignForUsers(ctx context.Context, req *dto.SignForUserRequest) (*dto.SignForUserResponse, error) {
	slot, err := s.resolveSignSlot(ctx, req)
	if err != nil {
		return nil, err
	}

	detail, err := s.loadAttendanceDetailForSign(ctx, slot)
	if err != nil {
		return nil, err
	}
	if s.manualOverrideRepo == nil {
		return nil, response.NewBizError(response.CodeInternalError, "人工代签覆盖仓储未初始化")
	}

	signable := make(map[uint]bool, len(detail.Users.Late)+len(detail.Users.NotArrived))
	for _, user := range detail.Users.Late {
		signable[user.ID] = true
	}
	for _, user := range detail.Users.NotArrived {
		signable[user.ID] = true
	}

	overrideSet := make(map[uint]bool)
	overrides, err := s.manualOverrideRepo.ListByDateSection(ctx, slot.date, slot.section)
	if err != nil {
		return nil, errs.WrapMsgErr("获取人工代签覆盖失败", err)
	}
	for _, override := range overrides {
		if override.OverrideType == attendanceOverrideTypeForceOnTime {
			overrideSet[override.UserID] = true
		}
	}

	for _, targetID := range req.TargetUserIDs {
		if signable[targetID] || overrideSet[targetID] {
			continue
		}
		return nil, response.NewBizError(response.CodeOperationFailed, "目标用户不在当前可代签范围内")
	}

	appliedAt := s.currentTime()
	slotDate := normalizeAttendanceDate(slot.date)
	tenantID := s.resolveOverrideTenantID(ctx, slot, req.TargetUserIDs)
	successIDs := make([]uint, 0, len(req.TargetUserIDs))
	for _, targetID := range req.TargetUserIDs {
		override := &model.AttendanceManualOverride{
			TenantID:     tenantID,
			Date:         slotDate,
			Week:         slot.week,
			Section:      slot.section,
			UserID:       targetID,
			OverrideType: attendanceOverrideTypeForceOnTime,
			OperatorID:   req.OperatorID,
			AppliedAt:    appliedAt,
		}
		if err := s.manualOverrideRepo.UpsertForceOnTime(ctx, override); err != nil {
			return nil, errs.WrapMsgErr("写入人工代签覆盖失败", err)
		}
		successIDs = append(successIDs, targetID)
	}

	if slot.record == nil {
		record, err := s.attendanceRecordRepo.FindByDateSection(ctx, slotDate, slot.section)
		switch {
		case err == nil:
			slot.record = record
			if record.Week > 0 {
				slot.week = record.Week
			}
		case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
			return nil, err
		}
	}

	if slot.record != nil {
		snapshotResp, err := s.GetAttendanceRecordFromDB(ctx, &dto.AttendanceDetailRequest{
			Date:    slotDate.Format("2006-01-02"),
			Week:    slot.week,
			Section: slot.section,
		})
		if err != nil {
			return nil, err
		}
		if err := s.SaveAttendanceRecord(ctx, snapshotResp); err != nil {
			return nil, err
		}
	}

	return &dto.SignForUserResponse{
		SuccessIDs: successIDs,
		FailedIDs:  []uint{},
	}, nil
}

// shouldUseAllAttendMode 判断是否应该使用"全体应到"模式
// 返回 true 的情况：
// 1. 当前为假期模式
// 2. 日期超出学期范围
// 3. 无学期配置
func (s *AttendanceRecordService) shouldUseAllAttendMode(ctx context.Context, date time.Time) bool {
	// 1. 检查是否为假期模式
	if s.schedulePeriodSrv != nil {
		currentMode, err := s.schedulePeriodSrv.GetCurrentMode(ctx)
		if err == nil && currentMode == model.ScheduleModeHoliday {
			return true
		}
	}

	// 2. 检查日期是否超出学期范围
	if s.semesterSrv != nil {
		semester, err := s.semesterSrv.GetActiveSemester(ctx)
		if err != nil {
			// 没有学期配置，使用全体应到模式
			return true
		}

		_, err = s.semesterSrv.CalculateWeekFromDate(semester, date)
		if err != nil {
			// 日期超出学期范围
			return true
		}
	}

	// 默认使用正常模式（应到 = 候选 - 有课）
	return false
}

// applyCarryForward 连续节次打卡顺延。
// 若用户在第 section-1 节正常打卡，且该节下课到本节上课的间隔 <= MaxCarryForwardGapMinutes，
// 则将上一节的打卡记录顺延到本节，视为本节正常打卡。
func (s *AttendanceRecordService) applyCarryForward(
	ctx context.Context,
	date time.Time,
	section int,
	periods []config.Period,
	shouldAttend []model.User,
	onTime []dto.AttendanceUserCheck,
) []dto.AttendanceUserCheck {
	// 仅第 2 节及以上才可能顺延
	if section <= 1 {
		return onTime
	}
	// 未配置或关闭顺延功能
	if s.scheduleCfg.MaxCarryForwardGapMinutes <= 0 {
		return onTime
	}
	// 节次越界保护
	if section > len(periods) {
		return onTime
	}

	// 计算上一节下课到本节上课的间隔（分钟）
	prevEnd, err1 := time.Parse("15:04", periods[section-2].End)
	currStart, err2 := time.Parse("15:04", periods[section-1].Start)
	if err1 != nil || err2 != nil {
		return onTime
	}
	gapMinutes := (currStart.Hour()*60 + currStart.Minute()) - (prevEnd.Hour()*60 + prevEnd.Minute())
	if gapMinutes > s.scheduleCfg.MaxCarryForwardGapMinutes {
		return onTime // 间隔过大（如午休），不顺延
	}

	// 查询上一节的考勤记录
	prevRecord, err := s.attendanceRecordRepo.FindByDateSection(ctx, date, section-1)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warnw("查询上一节考勤记录失败，跳过顺延",
				"date", date, "prevSection", section-1, "error", err)
		}
		return onTime // 无上一节记录，不顺延
	}

	// 解析上一节正常打卡人员
	var prevOnTime []dto.StoredUserCheck
	if err := json.Unmarshal([]byte(prevRecord.OnTimeIDs), &prevOnTime); err != nil || len(prevOnTime) == 0 {
		return onTime
	}

	// 构建索引
	currentOnTimeSet := make(map[uint]bool, len(onTime))
	for _, u := range onTime {
		currentOnTimeSet[u.ID] = true
	}
	shouldAttendMap := make(map[uint]model.User, len(shouldAttend))
	for _, u := range shouldAttend {
		shouldAttendMap[u.ID] = u
	}

	// 顺延：上一节正常打卡 且 本节应到 且 本节尚未打卡
	for _, prev := range prevOnTime {
		if currentOnTimeSet[prev.ID] {
			continue
		}
		u, inShouldAttend := shouldAttendMap[prev.ID]
		if !inShouldAttend {
			continue
		}
		onTime = append(onTime, dto.AttendanceUserCheck{
			ID:        u.ID,
			Name:      u.Name,
			Avatar:    u.Avatar,
			CheckTime: time.Unix(prev.CheckTime, 0),
		})
		currentOnTimeSet[u.ID] = true
	}

	return onTime
}
