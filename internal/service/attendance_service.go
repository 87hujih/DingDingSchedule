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
	"schedule_server/pkg/weekutil"

	"gorm.io/gorm"
)

// AttendanceService 考勤服务（从 ScheduleService 中拆分）。
type AttendanceService struct {
	repo        repository.AttendanceRepository
	dingClient  *dingtalk.Client
	scheduleCfg config.Schedule
}

func NewAttendanceService(repo repository.AttendanceRepository, dingClient *dingtalk.Client, scheduleCfg config.Schedule) *AttendanceService {
	return &AttendanceService{
		repo:        repo,
		dingClient:  dingClient,
		scheduleCfg: scheduleCfg,
	}
}

// GetCourseAttendanceStatus 计算某节课的应到/请假人员（应到=该时段无课的用户）。
//
// deptIDs：部门过滤条件（可选）
// - deptIDs 为空(nil 或 len=0)：按“全体参与考勤用户（users.status=1）”计算
// - deptIDs 非空：按“传入部门的并集用户（users.status=1）”计算
func (s *AttendanceService) GetCourseAttendanceStatus(
	ctx context.Context,
	viewerID uint,
	courseID uint,
	week int,
	deptIDs []int64,
) (*dto.CourseAttendanceStatusResponse, error) {
	sess, err := s.getCourseSessionForStatus(ctx, viewerID, courseID, week)
	if err != nil {
		return nil, err
	}

	// 1) 先拿到候选范围（全部 or 指定部门并集），再用课表剔除“本节有课”的用户，得到应到名单
	shouldArriveUsers, shouldArriveItems, err := s.computeShouldArriveUsersByDeptFilter(
		ctx,
		sess.Course.Semester,
		sess.Course.DayOfWeek,
		sess.Course.Section,
		sess.Week,
		deptIDs,
	)
	if err != nil {
		return nil, err
	}

	// 2) 在应到名单上调用钉钉请假接口，筛出本节时间窗口内请假的人（返回只含用户信息，不含请假明细）
	onLeaveItems, err := s.computeOnLeaveUserItems(ctx, shouldArriveUsers, sess.SessionStart, sess.SessionEnd)
	if err != nil {
		return nil, err
	}

	return &dto.CourseAttendanceStatusResponse{
		CourseID:     sess.Course.ID,
		Semester:     sess.Course.Semester,
		Week:         sess.Week,
		DayOfWeek:    sess.Course.DayOfWeek,
		Section:      sess.Course.Section,
		ShouldArrive: shouldArriveItems,
		OnLeave:      onLeaveItems,
	}, nil
}

// GetCourseUserLeaveDetail 获取某用户在某课程对应课节时间窗口内的请假明细（用于“点人看详情”）。
func (s *AttendanceService) GetCourseUserLeaveDetail(
	ctx context.Context,
	viewerID uint,
	viewerRole int,
	courseID uint,
	userID uint,
	week int,
) (*dto.CourseUserLeaveDetailResponse, error) {
	if userID == 0 {
		return nil, response.ErrInvalidParamWithMsg("用户ID无效")
	}

	sess, err := s.getCourseSessionForAttendance(ctx, viewerID, viewerRole, courseID, week)
	if err != nil {
		return nil, err
	}

	// 权限校验：目标用户必须在可见范围内
	if _, err := s.resolveTargetUserID(ctx, viewerID, viewerRole, userID); err != nil {
		return nil, err
	}

	dingUserID, err := s.getUserDingUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	leaveRecords, err := s.dingClient.GetLeaveStatus(ctx, []string{dingUserID}, sess.SessionStart, sess.SessionEnd)
	if err != nil {
		return nil, errs.WrapMsgErr("获取钉钉请假详情失败", err)
	}

	return &dto.CourseUserLeaveDetailResponse{
		CourseID:     sess.Course.ID,
		UserID:       userID,
		Week:         sess.Week,
		SessionStart: sess.SessionStart,
		SessionEnd:   sess.SessionEnd,
		Items:        toCourseLeaveRecordItems(leaveRecords, dingUserID, sess.SessionStart, sess.SessionEnd),
	}, nil
}

type courseSession struct {
	Course       *model.Course
	Week         int
	SessionStart time.Time
	SessionEnd   time.Time
}

// getCourseSessionForStatus 用于“课节状态”接口：仅要求已登录，不做任何角色/权限判定。
func (s *AttendanceService) getCourseSessionForStatus(
	ctx context.Context,
	viewerID uint,
	courseID uint,
	week int,
) (*courseSession, error) {
	if viewerID == 0 {
		return nil, response.ErrForbidden()
	}
	if courseID == 0 {
		return nil, response.ErrInvalidParamWithMsg("课程ID无效")
	}
	if s.dingClient == nil {
		return nil, response.NewBizError(response.CodeInternalError, "钉钉客户端未初始化")
	}

	course, err := s.getCourseOrNotFound(ctx, courseID)
	if err != nil {
		return nil, err
	}

	sem := (*model.Semester)(nil)
	week, sem, err = s.resolveWeekInSemester(ctx, course.Semester, week)
	if err != nil {
		return nil, err
	}
	if !weekutil.ContainsWeek(course.WeekList, week) {
		return nil, response.ErrInvalidParamWithMsg("该周没有这门课程")
	}

	sessionStart, sessionEnd, err := s.courseSessionTimeRange(sem.StartDate, week, course.DayOfWeek, course.Section)
	if err != nil {
		return nil, err
	}

	return &courseSession{
		Course:       course,
		Week:         week,
		SessionStart: sessionStart,
		SessionEnd:   sessionEnd,
	}, nil
}

// getCourseSessionForAttendance 统一处理“课程+权限+周次+时间窗口”的公共逻辑。
func (s *AttendanceService) getCourseSessionForAttendance(
	ctx context.Context,
	viewerID uint,
	viewerRole int,
	courseID uint,
	week int,
) (*courseSession, error) {
	if viewerID == 0 {
		return nil, response.ErrForbidden()
	}
	if courseID == 0 {
		return nil, response.ErrInvalidParamWithMsg("课程ID无效")
	}
	if s.dingClient == nil {
		return nil, response.NewBizError(response.CodeInternalError, "钉钉客户端未初始化")
	}

	course, err := s.getCourseOrNotFound(ctx, courseID)
	if err != nil {
		return nil, err
	}

	// 权限校验：所有登录用户可查看，但需属于课程所属用户的部门（管理员放行）
	if err := s.ensureCourseAttendanceViewerAllowed(ctx, viewerID, viewerRole, course.UserID); err != nil {
		return nil, err
	}

	sem := (*model.Semester)(nil)
	week, sem, err = s.resolveWeekInSemester(ctx, course.Semester, week)
	if err != nil {
		return nil, err
	}
	if !weekutil.ContainsWeek(course.WeekList, week) {
		return nil, response.ErrInvalidParamWithMsg("该周没有这门课程")
	}

	sessionStart, sessionEnd, err := s.courseSessionTimeRange(sem.StartDate, week, course.DayOfWeek, course.Section)
	if err != nil {
		return nil, err
	}

	return &courseSession{
		Course:       course,
		Week:         week,
		SessionStart: sessionStart,
		SessionEnd:   sessionEnd,
	}, nil
}

// computeShouldArriveUsersByDeptFilter 计算应到名单（本节无课人员）。
// - deptIDs 为空：不按部门筛选，使用全部参与考勤用户（status=1）
// - deptIDs 非空：仅使用这些部门下参与考勤的用户
func (s *AttendanceService) computeShouldArriveUsersByDeptFilter(
	ctx context.Context,
	semester string,
	dayOfWeek int,
	section int,
	week int,
	deptIDs []int64,
) ([]model.User, []dto.CourseAttendanceUserItem, error) {
	// 候选人集合：全体 or 指定部门并集
	activeUsers, err := s.listActiveUsersByDeptIDs(ctx, deptIDs)
	if err != nil {
		return nil, nil, err
	}
	// 忙碌人集合：本周同一天同节次有课的人（会结合 weekList 二次过滤）
	busyUsers, err := s.busyUserSetForSlot(ctx, activeUsers, semester, dayOfWeek, section, week)
	if err != nil {
		return nil, nil, err
	}

	// 应到 = 候选 - 忙碌
	shouldArriveUsers := filterUsersByExclude(activeUsers, busyUsers)
	return shouldArriveUsers, toAttendanceUserItems(shouldArriveUsers, nil), nil
}

// listActiveUsersByDeptIDs 获取候选用户（参与考勤 status=1）。
// - deptIDs 为空：返回全部用户（由 repo.ListUsersByScope 内部逻辑决定为“不限制”）
// - deptIDs 非空：返回这些部门的并集用户（会 DISTINCT 去重）
func (s *AttendanceService) listActiveUsersByDeptIDs(ctx context.Context, deptIDs []int64) ([]model.User, error) {
	users, err := s.repo.ListUsersByScope(ctx, deptIDs, nil)
	if err != nil {
		return nil, errs.WrapMsgErr("获取用户列表失败", err)
	}

	active := make([]model.User, 0, len(users))
	for _, u := range users {
		if u.Status == 1 {
			active = append(active, u)
		}
	}
	return active, nil
}

// busyUserSetForSlot 计算“忙”的用户集合：在指定学期、星期、节次，并且本周确实有课的用户。
func (s *AttendanceService) busyUserSetForSlot(
	ctx context.Context,
	users []model.User,
	semester string,
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

	courses, err := s.repo.ListCoursesByUsersSemesterDaySection(ctx, ids, semester, dayOfWeek, section)
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
// - 只会对 users 中“已绑定钉钉”的用户（DingUserID 非空）发起钉钉请假查询。
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

	dingUserIDs := make([]string, 0, len(users))
	for _, u := range users {
		if u.DingUserID != "" {
			dingUserIDs = append(dingUserIDs, u.DingUserID)
		}
	}
	if len(dingUserIDs) == 0 {
		return []dto.CourseAttendanceUserItem{}, nil
	}

	leaveRecords, err := s.dingClient.GetLeaveStatus(ctx, dingUserIDs, sessionStart, sessionEnd)
	if err != nil {
		return nil, errs.WrapMsgErr("获取钉钉请假信息失败", err)
	}

	onLeaveSet := make(map[string]struct{}, len(leaveRecords))
	for _, rec := range leaveRecords {
		if timeOverlaps(rec.StartAt, rec.EndAt, sessionStart, sessionEnd) {
			onLeaveSet[rec.DingUserID] = struct{}{}
		}
	}

	items := make([]dto.CourseAttendanceUserItem, 0)
	for _, u := range users {
		if _, ok := onLeaveSet[u.DingUserID]; !ok {
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

// getCourseOrNotFound 查询课程信息；若课程不存在则返回“资源不存在”的业务错误。
func (s *AttendanceService) getCourseOrNotFound(ctx context.Context, courseID uint) (*model.Course, error) {
	course, err := s.repo.GetCourseByID(ctx, courseID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.ErrNotFoundWithMsg("课程不存在")
		}
		return nil, errs.WrapMsgErr("查询课程失败", err)
	}
	return course, nil
}

// resolveWeekInSemester 根据学期配置解析最终周次：
// - week<=0 时自动取当前周
// - 校验 week 在 [1,totalWeek] 范围内
func (s *AttendanceService) resolveWeekInSemester(ctx context.Context, semesterName string, week int) (int, *model.Semester, error) {
	sem, err := s.repo.GetSemesterByName(ctx, semesterName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, response.ErrInvalidParamWithMsg("学期不存在，请先配置学期信息")
		}
		return 0, nil, errs.WrapMsgErr("获取学期信息失败", err)
	}

	if week <= 0 {
		week = weekutil.CurrentWeek(sem.StartDate, sem.TotalWeek)
	}
	if week <= 0 || week > sem.TotalWeek {
		return 0, nil, response.ErrInvalidParamWithMsg("周次无效")
	}
	return week, sem, nil
}

// courseSessionTimeRange 根据学期第一周周一日期、周次、星期与节次，计算该课节的实际起止时间。
// - semesterStart: 学期第一周周一（来自 Semester.StartDate）
// - week: 第几周（从 1 开始）
// - dayOfWeek: 星期几（1-7，1=周一）
// - section: 大节次（1=第1-2节, 2=第3-4节...），与配置 schedule.periods 顺序对应
func (s *AttendanceService) courseSessionTimeRange(semesterStart time.Time, week int, dayOfWeek int, section int) (time.Time, time.Time, error) {
	if week <= 0 || dayOfWeek < 1 || dayOfWeek > 7 || section <= 0 {
		return time.Time{}, time.Time{}, response.ErrInvalidParamWithMsg("课程时间参数无效")
	}

	periods := s.scheduleCfg.Periods
	if len(periods) == 0 || section > len(periods) {
		return time.Time{}, time.Time{}, response.NewBizError(response.CodeInternalError, "节次作息配置缺失")
	}

	period := periods[section-1]
	startClock, err := time.Parse("15:04", period.Start)
	if err != nil {
		return time.Time{}, time.Time{}, response.NewBizError(response.CodeInternalError, "节次作息配置开始时间格式错误")
	}
	endClock, err := time.Parse("15:04", period.End)
	if err != nil {
		return time.Time{}, time.Time{}, response.NewBizError(response.CodeInternalError, "节次作息配置结束时间格式错误")
	}

	// 计算该周该天的日期
	daysFromStart := (week-1)*7 + (dayOfWeek - 1)
	loc := semesterStart.Location()
	base := time.Date(semesterStart.Year(), semesterStart.Month(), semesterStart.Day(), 0, 0, 0, 0, loc)
	date := base.AddDate(0, 0, daysFromStart)

	startAt := time.Date(date.Year(), date.Month(), date.Day(), startClock.Hour(), startClock.Minute(), 0, 0, loc)
	endAt := time.Date(date.Year(), date.Month(), date.Day(), endClock.Hour(), endClock.Minute(), 0, 0, loc)
	if endAt.Before(startAt) {
		// 兜底：若结束时间早于开始时间，认为跨天
		endAt = endAt.Add(24 * time.Hour)
	}

	return startAt, endAt, nil
}

// ensureCourseAttendanceViewerAllowed 允许“所有登录用户”查看课节考勤，但需同部门（管理员放行）。
func (s *AttendanceService) ensureCourseAttendanceViewerAllowed(ctx context.Context, viewerID uint, viewerRole int, courseOwnerID uint) error {
	if viewerID == 0 || courseOwnerID == 0 {
		return response.ErrForbidden()
	}
	// 管理员及以上放行
	if viewerRole >= consts.RoleLabAdmin {
		return nil
	}
	// 自己查看自己的课程
	if viewerID == courseOwnerID {
		return nil
	}

	viewerDeptIDs, err := s.repo.FindUserDepartmentIDs(ctx, viewerID)
	if err != nil {
		return errs.WrapMsgErr("查询用户部门失败", err)
	}
	ownerDeptIDs, err := s.repo.FindUserDepartmentIDs(ctx, courseOwnerID)
	if err != nil {
		return errs.WrapMsgErr("查询课程所属用户部门失败", err)
	}

	if !hasDeptIntersection(viewerDeptIDs, ownerDeptIDs) {
		return response.ErrForbidden()
	}
	return nil
}

// resolveTargetUserID 校验访问权限并返回实际查询的用户ID（用于“点人看详情”）。
func (s *AttendanceService) resolveTargetUserID(ctx context.Context, viewerID uint, viewerRole int, targetUserID uint) (uint, error) {
	if viewerID == 0 {
		return 0, response.ErrForbidden()
	}

	// 未指定目标或目标即为自己
	if targetUserID == 0 || targetUserID == viewerID {
		return viewerID, nil
	}

	// 管理员及以上可直接查看任意用户
	if viewerRole >= consts.RoleLabAdmin {
		return targetUserID, nil
	}

	// 小组长只能查看同组成员（部门交集）
	if viewerRole >= consts.RoleGroupLead {
		viewerDeptIDs, err := s.repo.FindUserDepartmentIDs(ctx, viewerID)
		if err != nil {
			return 0, err
		}
		if len(viewerDeptIDs) == 0 {
			return 0, response.ErrForbidden()
		}

		targetDeptIDs, err := s.repo.FindUserDepartmentIDs(ctx, targetUserID)
		if err != nil {
			return 0, err
		}
		if len(targetDeptIDs) == 0 {
			return 0, response.ErrForbidden()
		}

		if hasDeptIntersection(viewerDeptIDs, targetDeptIDs) {
			return targetUserID, nil
		}
		return 0, response.ErrForbidden()
	}

	// 普通成员无权查看他人
	return 0, response.ErrForbidden()
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
