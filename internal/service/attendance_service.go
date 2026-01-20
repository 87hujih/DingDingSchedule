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
	repo        repository.AttendanceRepository
	dingMgr     *DingTalkClientManager
	scheduleCfg config.Schedule
	logger      *zap.SugaredLogger
}

func NewAttendanceService(repo repository.AttendanceRepository, dingMgr *DingTalkClientManager, scheduleCfg config.Schedule, logger *zap.SugaredLogger) *AttendanceService {
	return &AttendanceService{
		repo:        repo,
		dingMgr:     dingMgr,
		scheduleCfg: scheduleCfg,
		logger:      logger,
	}
}

// GetSlotAttendanceStatus 计算指定日期+周次+节次的应到/请假人员（不依赖 courseID）。
//
// deptIDs：部门过滤条件（可选）
// - deptIDs 为空(nil 或 len=0)：按“全体参与考勤用户（users.status=1）”计算
// - deptIDs 非空：按“传入部门的并集用户（users.status=1）”计算
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
	dayOfWeek := scheduleutil.WeekdayMon1Sun7(date)
	sessionStart, sessionEnd, err := scheduleutil.CalculateSlotTime(date, section, s.scheduleCfg.Periods)
	if err != nil {
		return nil, response.NewBizError(response.CodeInternalError, err.Error())
	}

	shouldArriveUsers, shouldArriveItems, err := s.computeShouldArriveUsersByDeptFilter(
		ctx,
		dayOfWeek,
		section,
		week,
		deptIDs,
	)
	if err != nil {
		return nil, err
	}

	onLeaveItems, err := s.computeOnLeaveUserItems(ctx, shouldArriveUsers, sessionStart, sessionEnd)
	if err != nil {
		return nil, err
	}

	return &dto.SlotAttendanceStatusResponse{
		Date:         date.Format("2006-01-02"),
		Week:         week,
		DayOfWeek:    dayOfWeek,
		Section:      section,
		ShouldArrive: shouldArriveItems,
		OnLeave:      onLeaveItems,
	}, nil
}

// GetSlotUserLeaveDetail 获取某用户在“时段(日期+节次)时间窗口”内的请假明细（不依赖 courseID）。
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
	sessionStart, sessionEnd, err := scheduleutil.CalculateSlotTime(date, section, s.scheduleCfg.Periods)
	if err != nil {
		return nil, response.NewBizError(response.CodeInternalError, err.Error())
	}

	dingUserID, err := s.getUserDingUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if s.dingMgr == nil {
		return nil, response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}
	_, dingClient, err := s.dingMgr.FromContext(ctx)
	if err != nil {
		return nil, response.NewBizError(response.CodeUnauthorized, "缺少租户信息")
	}

	leaveRecords, err := dingClient.GetLeaveStatus(ctx, []string{dingUserID}, sessionStart, sessionEnd)
	if err != nil {
		s.logger.Errorw("获取钉钉请假详情失败",
			"userID", userID,
			"dingUserID", dingUserID,
			"sessionStart", sessionStart,
			"sessionEnd", sessionEnd,
			"error", err,
		)
		return nil, errs.WrapMsgErr("获取钉钉请假详情失败", err)
	}

	return &dto.SlotUserLeaveDetailResponse{
		UserID:       userID,
		Week:         week,
		Date:         date.Format("2006-01-02"),
		DayOfWeek:    scheduleutil.WeekdayMon1Sun7(date),
		Section:      section,
		SessionStart: sessionStart,
		SessionEnd:   sessionEnd,
		Items:        toCourseLeaveRecordItems(leaveRecords, dingUserID, sessionStart, sessionEnd),
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
// - deptIDs 为空：不按部门筛选，使用全部参与考勤用户（status=1）
// - deptIDs 非空：仅使用这些部门下参与考勤的用户
func (s *AttendanceService) computeShouldArriveUsersByDeptFilter(
	ctx context.Context,
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
	busyUsers, err := s.busyUserSetForSlot(ctx, activeUsers, dayOfWeek, section, week)
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

	if s.dingMgr == nil {
		return nil, response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}
	_, dingClient, err := s.dingMgr.FromContext(ctx)
	if err != nil {
		return nil, response.NewBizError(response.CodeUnauthorized, "缺少租户信息")
	}

	leaveRecords, err := dingClient.GetLeaveStatus(ctx, dingUserIDs, sessionStart, sessionEnd)
	if err != nil {
		s.logger.Errorw("获取钉钉请假信息失败",
			"userCount", len(dingUserIDs),
			"sessionStart", sessionStart,
			"sessionEnd", sessionEnd,
			"error", err,
		)
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
