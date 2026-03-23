package service

import (
	"context"
	"errors"
	"time"

	"schedule_server/config"
	"schedule_server/internal/consts"
	"schedule_server/internal/dto"
	"schedule_server/internal/errs"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
	"schedule_server/pkg/dingtalk"
	"schedule_server/pkg/scheduleutil"
	"schedule_server/pkg/weekutil"

	"go.uber.org/zap"
)

// AttendanceService 考勤服务（从 ScheduleService 中拆分）。
type AttendanceService struct {
	repo              repository.AttendanceRepository
	leaveApprovalRepo repository.LeaveApprovalRepository
	userRepo          repository.UserRepository
	restDayRepo       repository.UserRestDayRepository
	dingMgr           *DingTalkClientManager
	schedulePeriodSrv *SchedulePeriodService // 从数据库读取作息时间
	semesterSrv       *SemesterService       // 学期服务
	scheduleCfg       config.Schedule        // 配置文件作为回退
	logger            *zap.SugaredLogger
}

func NewAttendanceService(repo repository.AttendanceRepository, leaveApprovalRepo repository.LeaveApprovalRepository, userRepo repository.UserRepository, restDayRepo repository.UserRestDayRepository, dingMgr *DingTalkClientManager, schedulePeriodSrv *SchedulePeriodService, semesterSrv *SemesterService, scheduleCfg config.Schedule, logger *zap.SugaredLogger) *AttendanceService {
	return &AttendanceService{
		repo:              repo,
		leaveApprovalRepo: leaveApprovalRepo,
		userRepo:          userRepo,
		restDayRepo:       restDayRepo,
		dingMgr:           dingMgr,
		schedulePeriodSrv: schedulePeriodSrv,
		semesterSrv:       semesterSrv,
		scheduleCfg:       scheduleCfg,
		logger:            logger,
	}
}

// GetSlotAttendanceStatus 计算指定日期+周次+节次的应到/请假人员（不依赖 courseID）。
//
// deptIDs：部门过滤条件（可选）
// - deptIDs 为空(nil 或 len=0)：按“用户启用且至少归属一个启用部门”的全体候选人计算
// - deptIDs 非空：按“传入部门中启用部门的并集用户，且用户状态启用”计算
func (s *AttendanceService) GetSlotAttendanceStatus(
	ctx context.Context,
	viewerID uint,
	date time.Time,
	week int,
	section int,
	deptIDs []int64,
) (*dto.SlotAttendanceStatusResponse, error) {
	if viewerID == 0 {
		return nil, response.ErrForbidden()
	}
	if err := s.validateSlotParams(date, week, section); err != nil {
		return nil, err
	}

	// 从数据库获取当前生效的作息时间配置
	periods := s.resolveActivePeriods(ctx)
	if len(periods) == 0 || section > len(periods) {
		return nil, response.ErrInvalidParamWithMsg("节次无效或作息配置缺失")
	}

	dayOfWeek := scheduleutil.WeekdayMon1Sun7(date)
	sessionStart, sessionEnd, err := scheduleutil.CalculateSlotTime(date, section, periods)
	if err != nil {
		return nil, response.NewBizError(response.CodeInternalError, err.Error())
	}

	// 判断是否使用"全体应到"模式（假期模式或超出学期时间）
	var activeUsers []model.User
	var shouldArriveUsers []model.User
	var shouldArriveItems []dto.CourseAttendanceUserItem
	var hasCourseUsers []model.User

	if s.shouldUseAllAttendMode(ctx, date) {
		// 全体应到模式：不排除有课人员
		activeUsers, err = s.listActiveUsersByDeptIDs(ctx, deptIDs)
		if err != nil {
			return nil, err
		}
		shouldArriveUsers = activeUsers
		shouldArriveItems = toAttendanceUserItems(shouldArriveUsers, nil)
	} else {
		// 正常模式：先获取候选人与原始有课名单，再按最终优先级重新归类
		activeUsers, shouldArriveUsers, shouldArriveItems, hasCourseUsers, err = s.computeShouldArriveUsersByDeptFilter(
			ctx,
			dayOfWeek,
			section,
			week,
			deptIDs,
		)
		if err != nil {
			return nil, err
		}
	}

	// 与 record/detail 保持一致：rest_day > leave > has_course > should_arrive
	onRestDayItems, err := s.computeOnRestDayUserItems(ctx, activeUsers, dayOfWeek)
	if err != nil {
		return nil, err
	}
	restDaySet := make(map[uint]struct{}, len(onRestDayItems))
	for _, item := range onRestDayItems {
		restDaySet[item.ID] = struct{}{}
	}

	nonRestUsers := filterUsersByExclude(activeUsers, restDaySet)
	onLeaveItems, err := s.computeOnLeaveUserItems(ctx, nonRestUsers, sessionStart, sessionEnd)
	if err != nil {
		return nil, err
	}
	leaveSet := make(map[uint]struct{}, len(onLeaveItems))
	for _, item := range onLeaveItems {
		leaveSet[item.ID] = struct{}{}
	}

	hasCourseUsers = filterUsersByExclude(hasCourseUsers, restDaySet)
	hasCourseUsers = filterUsersByExclude(hasCourseUsers, leaveSet)
	hasCourseItems := toAttendanceUserItems(hasCourseUsers, nil)

	shouldArriveUsers = filterUsersByExclude(shouldArriveUsers, restDaySet)
	shouldArriveUsers = filterUsersByExclude(shouldArriveUsers, leaveSet)
	shouldArriveItems = toAttendanceUserItems(shouldArriveUsers, nil)

	// 批量查询部门名称并填充
	userIDs := make([]uint, 0, len(shouldArriveUsers)+len(onLeaveItems)+len(onRestDayItems)+len(hasCourseUsers))
	for _, u := range shouldArriveUsers {
		userIDs = append(userIDs, u.ID)
	}
	for _, item := range onLeaveItems {
		userIDs = append(userIDs, item.ID)
	}
	for _, item := range onRestDayItems {
		userIDs = append(userIDs, item.ID)
	}
	for _, u := range hasCourseUsers {
		userIDs = append(userIDs, u.ID)
	}
	deptNameMap, err := s.userRepo.GetUserDepartmentNames(ctx, userIDs)
	if err != nil {
		s.logger.Warnw("查询部门名称失败，部门信息将为空", "error", err)
		deptNameMap = make(map[uint]string)
	}
	for i := range shouldArriveItems {
		shouldArriveItems[i].DeptName = deptNameMap[shouldArriveItems[i].ID]
	}
	for i := range onLeaveItems {
		onLeaveItems[i].DeptName = deptNameMap[onLeaveItems[i].ID]
	}
	for i := range onRestDayItems {
		onRestDayItems[i].DeptName = deptNameMap[onRestDayItems[i].ID]
	}
	for i := range hasCourseItems {
		hasCourseItems[i].DeptName = deptNameMap[hasCourseItems[i].ID]
	}

	if shouldArriveItems == nil {
		shouldArriveItems = []dto.CourseAttendanceUserItem{}
	}
	if onLeaveItems == nil {
		onLeaveItems = []dto.CourseAttendanceUserItem{}
	}
	if onRestDayItems == nil {
		onRestDayItems = []dto.CourseAttendanceUserItem{}
	}
	if hasCourseItems == nil {
		hasCourseItems = []dto.CourseAttendanceUserItem{}
	}

	return &dto.SlotAttendanceStatusResponse{
		Date:         date.Format("2006-01-02"),
		Week:         week,
		DayOfWeek:    dayOfWeek,
		Section:      section,
		ShouldArrive: shouldArriveItems,
		OnLeave:      onLeaveItems,
		OnRestDay:    onRestDayItems,
		HasCourse:    hasCourseItems,
	}, nil
}

// GetWeekSlotsAttendanceSummary 计算整周每个时段的考勤汇总数量（周视图专用）。
//
// 优化策略：所有公共数据（作息、模式、用户、课表、请假、休息日）各只查询一次，
// 7天×N节的 slot 计算全部在内存中完成，DB 查询固定为 ~7 次。
func (s *AttendanceService) GetWeekSlotsAttendanceSummary(
	ctx context.Context,
	viewerID uint,
	week int,
	startDate time.Time,
	deptIDs []int64,
) (*dto.WeekSlotsSummaryResponse, error) {
	if viewerID == 0 {
		return nil, response.ErrForbidden()
	}
	if startDate.IsZero() || week <= 0 {
		return nil, response.ErrInvalidParamWithMsg("参数无效")
	}

	// ── Step 1: 共享数据批量加载（~7 次 DB 查询）────────────────────────────

	periods := s.resolveActivePeriods(ctx)
	if len(periods) == 0 {
		return nil, response.ErrInvalidParamWithMsg("作息配置缺失")
	}

	useAllMode := s.shouldUseAllAttendMode(ctx, startDate)

	activeUsers, err := s.listActiveUsersByDeptIDs(ctx, deptIDs)
	if err != nil {
		return nil, err
	}

	totalSlots := 7 * len(periods)
	slots := make([]dto.SlotSummaryItem, 0, totalSlots)

	// 无活跃用户时直接返回全零 slots
	if len(activeUsers) == 0 {
		for day := 1; day <= 7; day++ {
			date := startDate.AddDate(0, 0, day-1)
			for sec := 1; sec <= len(periods); sec++ {
				slots = append(slots, dto.SlotSummaryItem{
					Date:      date.Format("2006-01-02"),
					DayOfWeek: day,
					Section:   sec,
				})
			}
		}
		return &dto.WeekSlotsSummaryResponse{Week: week, Slots: slots}, nil
	}

	userIDs := make([]uint, 0, len(activeUsers))
	for _, u := range activeUsers {
		userIDs = append(userIDs, u.ID)
	}

	// 整周课程：一次查全部 day，按 (dayOfWeek, section) 分组到内存
	type daySectionKey struct{ day, sec int }
	allCourses, err := s.repo.ListCoursesByUsersDays(ctx, userIDs, []int{1, 2, 3, 4, 5, 6, 7})
	if err != nil {
		return nil, errs.WrapMsgErr("获取整周课表失败", err)
	}
	coursesMap := make(map[daySectionKey][]model.Course, len(allCourses))
	for _, c := range allCourses {
		k := daySectionKey{c.DayOfWeek, c.Section}
		coursesMap[k] = append(coursesMap[k], c)
	}

	// 整周请假：一次查全部，按 userID 分组到内存
	weekEnd := startDate.AddDate(0, 0, 7)
	allLeaves, err := s.leaveApprovalRepo.ListApprovedByUserIDs(ctx, userIDs, startDate, weekEnd)
	if err != nil {
		return nil, errs.WrapMsgErr("获取整周请假记录失败", err)
	}
	leavesByUserID := make(map[uint][]model.LeaveApproval, len(allLeaves))
	for _, l := range allLeaves {
		leavesByUserID[l.UserID] = append(leavesByUserID[l.UserID], l)
	}

	// 整周休息日：一次查全部，按 dayOfWeek 分组到内存
	allRestDays, err := s.restDayRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		s.logger.Warnw("查询休息日记录失败，跳过休息日逻辑", "error", err)
		allRestDays = nil
	}
	restDayMap := make(map[int]map[uint]struct{})
	for _, rd := range allRestDays {
		if rd.DayOfWeek == nil {
			continue
		}
		d := *rd.DayOfWeek
		if restDayMap[d] == nil {
			restDayMap[d] = make(map[uint]struct{})
		}
		restDayMap[d][rd.UserID] = struct{}{}
	}

	// ── Step 2: 纯内存计算，遍历 7天 × N节（零 DB 开销）────────────────────

	for day := 1; day <= 7; day++ {
		date := startDate.AddDate(0, 0, day-1)
		restSet := restDayMap[day] // nil map 读取安全，不会 panic

		for sec := 1; sec <= len(periods); sec++ {
			sessionStart, sessionEnd, calcErr := scheduleutil.CalculateSlotTime(date, sec, periods)
			if calcErr != nil {
				s.logger.Warnw("计算时段时间失败，跳过该节", "day", day, "section", sec, "error", calcErr)
				continue
			}

			// 有课用户：本周该节确实排有课程
			busySet := make(map[uint]struct{})
			if !useAllMode {
				for _, c := range coursesMap[daySectionKey{day, sec}] {
					if weekutil.ContainsWeek(c.WeekList, week) {
						busySet[c.UserID] = struct{}{}
					}
				}
			}

			// 按优先级（有课 > 休息 > 请假 > 应到）逐一归类
			hasCourseCount, onRestCount, onLeaveCount, shouldArriveCount := 0, 0, 0, 0
			for _, u := range activeUsers {
				if _, busy := busySet[u.ID]; busy {
					hasCourseCount++
					continue
				}
				if _, rest := restSet[u.ID]; rest {
					onRestCount++
					continue
				}
				onLeave := false
				for _, leave := range leavesByUserID[u.ID] {
					if timeOverlaps(leave.StartAt, leave.EndAt, sessionStart, sessionEnd) {
						onLeave = true
						break
					}
				}
				if onLeave {
					onLeaveCount++
					continue
				}
				shouldArriveCount++
			}

			slots = append(slots, dto.SlotSummaryItem{
				Date:              date.Format("2006-01-02"),
				DayOfWeek:         day,
				Section:           sec,
				ShouldArriveCount: shouldArriveCount,
				OnLeaveCount:      onLeaveCount,
				OnRestDayCount:    onRestCount,
				HasCourseCount:    hasCourseCount,
			})
		}
	}

	return &dto.WeekSlotsSummaryResponse{Week: week, Slots: slots}, nil
}

// GetSlotUserLeaveDetail 获取某用户在”时段(日期+节次)时间窗口”内的请假明细（不依赖 courseID）。
//
// 说明：
// - 仅管理员可访问（路由层会拦截，这里再做一次防御性校验）
// - 时间窗口算法与 GetSlotAttendanceStatus 完全一致：由 date + section + 作息配置计算
func (s *AttendanceService) GetSlotUserLeaveDetail(
	ctx context.Context,
	viewerID uint,
	viewerRole int,
	userID uint,
	week int,
	date time.Time,
	section int,
) (*dto.SlotUserLeaveDetailResponse, error) {
	if viewerID == 0 || viewerRole < consts.RoleAdmin {
		return nil, response.ErrForbidden()
	}
	if err := s.validateSlotParams(date, week, section); err != nil {
		return nil, err
	}

	// 从数据库获取当前生效的作息时间配置
	periods := s.resolveActivePeriods(ctx)
	if len(periods) == 0 || section > len(periods) {
		return nil, response.ErrInvalidParamWithMsg("节次无效或作息配置缺失")
	}

	sessionStart, sessionEnd, err := scheduleutil.CalculateSlotTime(date, section, periods)
	if err != nil {
		return nil, response.NewBizError(response.CodeInternalError, err.Error())
	}

	// 从本地数据库查询请假记录，而不是调用钉钉 API
	leaveRecords, err := s.leaveApprovalRepo.ListOverlappingByUserID(ctx, userID, sessionStart, sessionEnd)
	if err != nil {
		s.logger.Errorw("查询本地请假记录失败",
			"userID", userID,
			"sessionStart", sessionStart,
			"sessionEnd", sessionEnd,
			"error", err,
		)
		return nil, errs.WrapMsgErr("查询请假记录失败", err)
	}

	return &dto.SlotUserLeaveDetailResponse{
		UserID:       userID,
		Week:         week,
		Date:         date.Format("2006-01-02"),
		DayOfWeek:    scheduleutil.WeekdayMon1Sun7(date),
		Section:      section,
		SessionStart: sessionStart,
		SessionEnd:   sessionEnd,
		Items:        toLeaveRecordItems(leaveRecords, sessionStart, sessionEnd),
	}, nil
}

// validateSlotParams 统一校验时段相关的通用参数
func (s *AttendanceService) validateSlotParams(date time.Time, week, section int) error {
	if s.dingMgr == nil {
		return response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}
	if date.IsZero() {
		return response.ErrInvalidParamWithMsg("date 不能为空")
	}
	if week <= 0 {
		return response.ErrInvalidParamWithMsg("week 不能为空")
	}
	if section <= 0 {
		return response.ErrInvalidParamWithMsg("section 无效")
	}
	return nil
}

// computeShouldArriveUsersByDeptFilter 计算应到名单（本节无课人员）。
// - deptIDs 为空：不按部门筛选，使用所有部门启用且用户状态启用的候选人
// - deptIDs 非空：仅使用这些启用部门下用户状态启用的候选人
// 返回值：候选用户列表、应到用户列表、应到用户DTO列表、有课用户列表
func (s *AttendanceService) computeShouldArriveUsersByDeptFilter(
	ctx context.Context,
	dayOfWeek int,
	section int,
	week int,
	deptIDs []int64,
) ([]model.User, []model.User, []dto.CourseAttendanceUserItem, []model.User, error) {
	// 候选人集合：全体 or 指定部门并集
	activeUsers, err := s.listActiveUsersByDeptIDs(ctx, deptIDs)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// 忙碌人集合：本周同一天同节次有课的人（会结合 weekList 二次过滤）
	busySet, err := s.busyUserSetForSlot(ctx, activeUsers, dayOfWeek, section, week)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// 应到 = 候选 - 忙碌
	shouldArriveUsers := filterUsersByExclude(activeUsers, busySet)

	// 有课用户列表
	busyUsers := make([]model.User, 0, len(busySet))
	for _, u := range activeUsers {
		if _, ok := busySet[u.ID]; ok {
			busyUsers = append(busyUsers, u)
		}
	}

	return activeUsers, shouldArriveUsers, toAttendanceUserItems(shouldArriveUsers, nil), busyUsers, nil
}

// listActiveUsersByDeptIDs 获取候选用户。
// - deptIDs 为空：返回用户状态启用且至少归属一个启用部门的用户
// - deptIDs 非空：返回指定启用部门下用户状态启用的并集用户
func (s *AttendanceService) listActiveUsersByDeptIDs(ctx context.Context, deptIDs []int64) ([]model.User, error) {
	users, err := s.userRepo.ListAttendanceCandidates(ctx, deptIDs)
	if err != nil {
		return nil, errs.WrapMsgErr("获取用户列表失败", err)
	}
	return users, nil
}

// busyUserSetForSlot 计算“忙”的用户集合：在指定星期、节次，并且本周确实有课的用户。
func (s *AttendanceService) busyUserSetForSlot(
	ctx context.Context,
	users []model.User,
	dayOfWeek int,
	section int,
	week int,
) (map[uint]struct{}, error) {
	ids := make([]uint, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	if len(ids) == 0 {
		return map[uint]struct{}{}, nil
	}

	courses, err := s.repo.ListCoursesByUsersDaySection(ctx, ids, dayOfWeek, section)
	if err != nil {
		return nil, errs.WrapMsgErr("获取用户课表失败", err)
	}

	busy := make(map[uint]struct{}, len(courses))
	for _, c := range courses {
		if weekutil.ContainsWeek(c.WeekList, week) {
			busy[c.UserID] = struct{}{}
		}
	}
	return busy, nil
}

// computeOnLeaveUserItems 在给定课节时间窗口内，筛出请假的用户列表（仅返回用户信息，不含请假明细）。
//
// 说明：
// - 从本地数据库查询请假记录（不再调用钉钉API）
// - 通过 timeOverlaps 判断请假记录与课节时间窗口是否重叠；重叠则认为该用户请假。
func (s *AttendanceService) computeOnLeaveUserItems(
	ctx context.Context,
	users []model.User,
	sessionStart time.Time,
	sessionEnd time.Time,
) ([]dto.CourseAttendanceUserItem, error) {
	if len(users) == 0 {
		return []dto.CourseAttendanceUserItem{}, nil
	}

	// 收集所有用户的本地ID
	userIDs := make([]uint, 0, len(users))
	for _, u := range users {
		userIDs = append(userIDs, u.ID)
	}

	// 从本地数据库查询请假记录
	leaveRecords, err := s.leaveApprovalRepo.ListApprovedByUserIDs(ctx, userIDs, sessionStart, sessionEnd)
	if err != nil {
		s.logger.Errorw("查询本地请假记录失败",
			"userCount", len(userIDs),
			"sessionStart", sessionStart,
			"sessionEnd", sessionEnd,
			"error", err,
		)
		return nil, errs.WrapMsgErr("查询请假记录失败", err)
	}

	// 构建请假用户集合（user_id -> 是否请假）
	onLeaveSet := make(map[uint]struct{}, len(leaveRecords))
	for _, rec := range leaveRecords {
		// 再次检查时间重叠（虽然SQL已经过滤，但这里做二次确认）
		if timeOverlaps(rec.StartAt, rec.EndAt, sessionStart, sessionEnd) {
			onLeaveSet[rec.UserID] = struct{}{}
		}
	}

	// 返回请假用户列表
	items := make([]dto.CourseAttendanceUserItem, 0)
	for _, u := range users {
		if _, ok := onLeaveSet[u.ID]; !ok {
			continue
		}
		items = append(items, dto.CourseAttendanceUserItem{
			ID:     u.ID,
			Name:   u.Name,
			Avatar: u.Avatar,
			Phone:  u.Phone,
		})
	}
	return items, nil
}

// getUserDingUserID 获取指定用户的钉钉用户ID（ding_user_id）。
// - 用户不存在：返回“用户不存在”业务错误
// - 用户未绑定钉钉：返回“用户未绑定钉钉”业务错误
func (s *AttendanceService) getUserDingUserID(ctx context.Context, userID uint) (string, error) {
	u, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return "", response.ErrUserNotFound()
		}
		return "", errs.WrapMsgErr("查询用户失败", err)
	}
	if u.DingUserID == "" {
		return "", response.NewBizError(response.CodeDingUserUnbind, "用户未绑定钉钉")
	}
	return u.DingUserID, nil
}

// toCourseLeaveRecordItems 将钉钉请假记录转换为“请假明细”返回结构，并按课节时间窗口过滤。
// - dingUserID 非空时：仅保留该用户的记录
// - 仅保留与 [sessionStart, sessionEnd) 有时间重叠的记录
func toCourseLeaveRecordItems(records []dingtalk.LeaveRecord, dingUserID string, sessionStart, sessionEnd time.Time) []dto.CourseLeaveRecordItem {
	items := make([]dto.CourseLeaveRecordItem, 0, len(records))
	for _, rec := range records {
		if dingUserID != "" && rec.DingUserID != dingUserID {
			continue
		}
		if !timeOverlaps(rec.StartAt, rec.EndAt, sessionStart, sessionEnd) {
			continue
		}
		items = append(items, dto.CourseLeaveRecordItem{
			LeaveType:       rec.LeaveType,
			StartAt:         rec.StartAt,
			EndAt:           rec.EndAt,
			DurationSeconds: rec.DurationSeconds,
			Status:          rec.Status,
			Remark:          rec.Remark,
		})
	}
	return items
}

// timeOverlaps 判断两个时间区间是否重叠（采用半开区间 [start, end)）。
func timeOverlaps(startA, endA, startB, endB time.Time) bool {
	// 半开区间 [start, end)，边界相接不算重叠
	return startA.Before(endB) && endA.After(startB)
}

// 封装人员接口
func toAttendanceUserItems(users []model.User, exclude map[uint]struct{}) []dto.CourseAttendanceUserItem {
	items := make([]dto.CourseAttendanceUserItem, 0, len(users))
	for _, u := range users {
		if _, ok := exclude[u.ID]; ok {
			continue
		}
		items = append(items, dto.CourseAttendanceUserItem{
			ID:     u.ID,
			Name:   u.Name,
			Avatar: u.Avatar,
			Phone:  u.Phone,
		})
	}
	return items
}

// 过滤人员，获取应到人员
func filterUsersByExclude(users []model.User, exclude map[uint]struct{}) []model.User {
	if len(users) == 0 {
		return []model.User{}
	}
	if len(exclude) == 0 {
		return users
	}
	out := make([]model.User, 0, len(users))
	for _, u := range users {
		if _, ok := exclude[u.ID]; ok {
			continue
		}
		out = append(out, u)
	}
	return out
}

// toLeaveRecordItems 将本地数据库的请假记录转换为 DTO（用于 GetSlotUserLeaveDetail）
func toLeaveRecordItems(records []model.LeaveApproval, sessionStart, sessionEnd time.Time) []dto.CourseLeaveRecordItem {
	items := make([]dto.CourseLeaveRecordItem, 0, len(records))
	for _, rec := range records {
		// 检查请假时间是否与时段重叠
		if !timeOverlaps(rec.StartAt, rec.EndAt, sessionStart, sessionEnd) {
			continue
		}

		// 计算重叠部分的时长
		overlapStart := rec.StartAt
		if sessionStart.After(overlapStart) {
			overlapStart = sessionStart
		}
		overlapEnd := rec.EndAt
		if sessionEnd.Before(overlapEnd) {
			overlapEnd = sessionEnd
		}
		durationSeconds := int64(overlapEnd.Sub(overlapStart).Seconds())

		items = append(items, dto.CourseLeaveRecordItem{
			UserName:        rec.UserName,
			LeaveType:       rec.LeaveType,
			StartAt:         rec.StartAt,
			EndAt:           rec.EndAt,
			DurationSeconds: durationSeconds,
			Status:          rec.ApproveStatus,
			Remark:          rec.Reason,
		})
	}
	return items
}

// resolveActivePeriods 获取当前生效的作息时间配置（优先从数据库，回退到配置文件）
func (s *AttendanceService) resolveActivePeriods(ctx context.Context) []config.Period {
	if s.schedulePeriodSrv != nil {
		periods, err := s.schedulePeriodSrv.GetActivePeriods(ctx)
		if err == nil && len(periods) > 0 {
			return periods
		}
	}
	return s.scheduleCfg.Periods
}

// shouldUseAllAttendMode 判断是否应该使用"全体应到"模式
// 返回 true 的情况：
// 1. 当前为假期模式
// 2. 日期超出学期范围
func (s *AttendanceService) shouldUseAllAttendMode(ctx context.Context, date time.Time) bool {
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

// computeOnRestDayUserItems 从应到人员中筛出今日休息日的用户
func (s *AttendanceService) computeOnRestDayUserItems(
	ctx context.Context,
	users []model.User,
	dayOfWeek int,
) ([]dto.CourseAttendanceUserItem, error) {
	if len(users) == 0 || s.restDayRepo == nil {
		return []dto.CourseAttendanceUserItem{}, nil
	}

	userIDs := make([]uint, 0, len(users))
	for _, u := range users {
		userIDs = append(userIDs, u.ID)
	}

	restDays, err := s.restDayRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		s.logger.Warnw("查询休息日记录失败", "error", err)
		return []dto.CourseAttendanceUserItem{}, nil
	}

	// 筛出今天是休息日的用户ID
	restDayUserSet := make(map[uint]struct{})
	for _, rd := range restDays {
		if *rd.DayOfWeek == dayOfWeek {
			restDayUserSet[rd.UserID] = struct{}{}
		}
	}

	if len(restDayUserSet) == 0 {
		return []dto.CourseAttendanceUserItem{}, nil
	}

	items := make([]dto.CourseAttendanceUserItem, 0, len(restDayUserSet))
	for _, u := range users {
		if _, ok := restDayUserSet[u.ID]; ok {
			items = append(items, dto.CourseAttendanceUserItem{
				ID:     u.ID,
				Name:   u.Name,
				Avatar: u.Avatar,
				Phone:  u.Phone,
			})
		}
	}
	return items, nil
}
