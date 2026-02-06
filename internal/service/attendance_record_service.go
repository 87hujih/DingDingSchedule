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
	"schedule_server/pkg/scheduleutil"
	"schedule_server/pkg/weekutil"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// AttendanceRecordService 考勤记录服务（打卡统计）
type AttendanceRecordService struct {
	userRepo             repository.UserRepository
	courseRepo           repository.CourseRepository
	leaveRepo            repository.LeaveApprovalRepository
	attendanceRecordRepo repository.AttendanceRecordRepository
	dingMgr              *DingTalkClientManager
	scheduleCfg          config.Schedule
	schedulePeriodSrv    *SchedulePeriodService
	logger               *zap.SugaredLogger
}

// NewAttendanceRecordService 创建考勤记录服务实例
func NewAttendanceRecordService(
	userRepo repository.UserRepository,
	courseRepo repository.CourseRepository,
	leaveRepo repository.LeaveApprovalRepository,
	attendanceRecordRepo repository.AttendanceRecordRepository,
	dingMgr *DingTalkClientManager,
	schedulePeriodSrv *SchedulePeriodService,
	scheduleCfg config.Schedule,
	logger *zap.SugaredLogger,
) *AttendanceRecordService {
	return &AttendanceRecordService{
		userRepo:             userRepo,
		courseRepo:           courseRepo,
		leaveRepo:            leaveRepo,
		attendanceRecordRepo: attendanceRecordRepo,
		dingMgr:              dingMgr,
		schedulePeriodSrv:    schedulePeriodSrv,
		scheduleCfg:          scheduleCfg,
		logger:               logger,
	}
}

// GetAttendanceDetail 获取考勤详情（核心方法）
// 查询指定日期、周次、节次的考勤情况，包括应到、正常打卡、请假、未到人员
func (s *AttendanceRecordService) GetAttendanceDetail(
	ctx context.Context,
	req *dto.AttendanceDetailRequest,
) (*dto.AttendanceDetailResponse, error) {
	resp, _, err := s.getAttendanceDetailWithLateUsers(ctx, req)
	return resp, err
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

	periods := s.resolveActivePeriods(ctx)

	// 2. 校验节次
	if req.Section <= 0 || req.Section > len(periods) {
		return nil, nil, response.ErrInvalidParamWithMsg("节次无效")
	}

	// 3. 计算时间窗口
	slotStart, slotEnd, err := scheduleutil.CalculateSlotTime(date, req.Section, periods)
	if err != nil {
		return nil, nil, response.NewBizError(response.CodeInternalError, err.Error())
	}

	// 4. 获取应到人员
	shouldAttend, err := s.getShouldAttendUsers(ctx, date, req.Week, req.Section, req.DeptIDs)
	if err != nil {
		return nil, nil, err
	}

	if len(shouldAttend) == 0 {
		return dto.NewAttendanceDetailResponse(
			req.Date, req.Week, req.Section,
			periods[req.Section-1].Start,
			periods[req.Section-1].End,
			shouldAttend,
			[]dto.AttendanceUserCheck{},
			[]dto.AttendanceUserLeave{},
			[]dto.AttendanceUserBasic{},
		), nil, nil
	}

	// 5. 获取打卡记录（只返回正常打卡的人）
	// 【修改】传入 section 参数用于计算打卡窗口
	onTime, lateUsers, err := s.getOnTimeUsers(ctx, shouldAttend, date, req.Section, slotStart, slotEnd)
	if err != nil {
		return nil, nil, err
	}

	// 6. 获取请假人员
	leave, err := s.getLeaveUsers(ctx, shouldAttend, slotStart, slotEnd)
	if err != nil {
		return nil, nil, err
	}

	// 7. 计算未到人员（应到 - 正常打卡 - 请假）
	notArrived := s.calculateNotArrived(shouldAttend, onTime, leave)

	// 8. 构建响应
	return dto.NewAttendanceDetailResponse(
		req.Date, req.Week, req.Section,
		periods[req.Section-1].Start,
		periods[req.Section-1].End,
		shouldAttend, onTime, leave, notArrived,
	), lateUsers, nil
}

// SaveAttendanceRecord 保存考勤记录到数据库
func (s *AttendanceRecordService) SaveAttendanceRecord(
	ctx context.Context,
	resp *dto.AttendanceDetailResponse,
) error {
	date, _ := time.ParseInLocation("2006-01-02", resp.Date, time.Local)

	// 序列化各类人员ID
	onTimeJSON, _ := s.serializeOnTimeUsers(resp.Users.OnTime)
	leaveJSON, _ := s.serializeLeaveUsers(resp.Users.Leave)
	notArrivedJSON, _ := s.serializeNotArrivedUsers(resp.Users.NotArrived)

	record := &model.AttendanceRecord{
		Date:          date,
		Week:          resp.Week,
		Section:       resp.Section,
		OnTimeIDs:     onTimeJSON,
		LeaveIDs:      leaveJSON,
		NotArrivedIDs: notArrivedJSON,
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

	slotStart := s.scheduleCfg.Periods[req.Section-1].Start
	slotEnd := s.scheduleCfg.Periods[req.Section-1].End

	return dto.NewAttendanceDetailResponseFromRecord(record, slotStart, slotEnd, userMap), nil
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

	periods := s.resolveActivePeriods(ctx)

	// 第2节及以后：从上一节的下课时间开始
	// 注意：这里使用配置文件作为回退方案，实际应该从数据库读取
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

// getShouldAttendUsers 获取应到人员（候选人 - 有课人员）
// deptIDs 为空时返回全部参与考勤用户，否则仅返回指定部门的用户
func (s *AttendanceRecordService) getShouldAttendUsers(
	ctx context.Context,
	date time.Time,
	week, section int,
	deptIDs []int64,
) ([]model.User, error) {
	// 1. 获取候选用户（按部门过滤或全部）
	candidates, err := s.userRepo.ListByScope(ctx, deptIDs, nil)
	if err != nil {
		return nil, errs.WrapMsgErr("获取候选用户失败", err)
	}

	// 2. 过滤出参与考勤的用户（status=1）
	activeUsers := make([]model.User, 0, len(candidates))
	for _, u := range candidates {
		if u.Status == 1 {
			activeUsers = append(activeUsers, u)
		}
	}

	if len(activeUsers) == 0 {
		return []model.User{}, nil
	}

	// 3. 获取该时段有课的人员
	dayOfWeek := scheduleutil.WeekdayMon1Sun7(date)
	userIDs := make([]uint, 0, len(activeUsers))
	for _, u := range activeUsers {
		userIDs = append(userIDs, u.ID)
	}

	courses, err := s.courseRepo.ListByUsersDaySection(ctx, userIDs, dayOfWeek, section)
	if err != nil {
		return nil, errs.WrapMsgErr("获取用户课表失败", err)
	}

	// 4. 过滤出本周有课的用户
	busyUserSet := make(map[uint]bool)
	for _, c := range courses {
		if weekutil.ContainsWeek(c.WeekList, week) {
			busyUserSet[c.UserID] = true
		}
	}

	// 5. 应到人员 = 候选人 - 有课人员
	shouldAttend := make([]model.User, 0, len(activeUsers))
	for _, u := range activeUsers {
		if !busyUserSet[u.ID] {
			shouldAttend = append(shouldAttend, u)
		}
	}

	return shouldAttend, nil
}

// getOnTimeUsers 获取正常打卡人员（在有效时间窗口内打卡的人）
func (s *AttendanceRecordService) getOnTimeUsers(
	ctx context.Context,
	users []model.User,
	date time.Time,
	section int, // 节次（用于计算打卡窗口）
	deadline time.Time, // 上课时间（打卡截止时间）
	slotEnd time.Time,
) ([]dto.AttendanceUserCheck, []model.User, error) {
	if len(users) == 0 {
		return []dto.AttendanceUserCheck{}, []model.User{}, nil
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
		return []dto.AttendanceUserCheck{}, []model.User{}, nil
	}

	// 获取钉钉客户端
	if s.dingMgr == nil {
		return nil, nil, response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}
	_, dingClient, err := s.dingMgr.FromContext(ctx)
	if err != nil {
		return nil, nil, response.NewBizError(response.CodeUnauthorized, "缺少租户信息")
	}

	// 【核心修改】计算有效打卡窗口
	windowStart, err := s.calculateCheckWindowStart(ctx, date, section)
	if err != nil {
		return nil, nil, errs.WrapMsgErr("计算打卡窗口失败", err)
	}

	// 查询打卡记录：从窗口开始到下课时间
	// 注意：仍然查询到下课时间，以便统计迟到人数
	queryStart := windowStart
	queryEnd := slotEnd

	records, err := dingClient.GetAttendanceRecords(ctx, dingUserIDs, queryStart, queryEnd)
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

	s.logger.Infow("打卡记录统计",
		"应到人数", len(users),
		"查询到的打卡记录数", len(records),
		"有效打卡人数", len(earliestCheck),
		"窗口开始", windowStart.Format("2006-01-02 15:04:05"),
		"截止时间", deadline.Format("2006-01-02 15:04:05"),
	)

	// 【核心修改】只返回在有效窗口内打卡的人
	onTime := make([]dto.AttendanceUserCheck, 0)
	lateUsers := make([]model.User, 0)
	lateCount := 0
	tooEarlyCount := 0

	for dingUserID, checkTime := range earliestCheck {
		user := userByDingID[dingUserID]
		if user == nil {
			s.logger.Warnw("找不到对应的用户", "dingUserID", dingUserID)
			continue
		}

		// 【新增】检查打卡时间是否在有效窗口内
		if checkTime.Before(windowStart) {
			tooEarlyCount++
			s.logger.Infow("打卡时间早于有效窗口",
				"用户", user.Name,
				"打卡时间", checkTime.Format("2006-01-02 15:04:05"),
				"窗口开始", windowStart.Format("2006-01-02 15:04:05"),
				"提前了", windowStart.Sub(checkTime).String(),
			)
			continue // 跳过这条打卡记录
		}

		// 判断是否迟到
		if checkTime.Before(deadline) || checkTime.Equal(deadline) {
			onTime = append(onTime, dto.AttendanceUserCheck{
				ID:        user.ID,
				Name:      user.Name,
				CheckTime: checkTime,
			})
			s.logger.Debugw("正常打卡",
				"用户", user.Name,
				"打卡时间", checkTime.Format("2006-01-02 15:04:05"),
				"窗口开始", windowStart.Format("2006-01-02 15:04:05"),
				"截止时间", deadline.Format("2006-01-02 15:04:05"),
			)
		} else {
			lateCount++
			lateUsers = append(lateUsers, *user)
			s.logger.Infow("打卡晚于截止时间",
				"用户", user.Name,
				"打卡时间", checkTime.Format("2006-01-02 15:04:05"),
				"截止时间", deadline.Format("2006-01-02 15:04:05"),
				"晚了", checkTime.Sub(deadline).String(),
			)
		}
	}

	s.logger.Infow("打卡统计结果",
		"正常打卡", len(onTime),
		"晚于截止时间", lateCount,
		"早于窗口", tooEarlyCount,
		"未打卡", len(users)-len(onTime)-lateCount,
	)

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
	content := fmt.Sprintf("你在%s %s考勤(%s-%s)迟到，请及时补签或联系管理员。", date, periodLabel, slotStart, slotEnd)
	if err := dingClient.SendWorkNoticeText(ctx, tenant.AgentID, dingUserIDs, content); err != nil {
		return errs.WrapMsgErr("发送钉钉迟到提醒失败", err)
	}

	s.logger.Infow("成功发送迟到提醒",
		"tenantId", tenant.ID,
		"lateUserIDs", dingUserIDs,
		"lateUserNames", lateUserNames,
		"date", date,
		"section", section,
		"content", content,
	)

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
	// 构建已处理用户集合
	processed := make(map[uint]bool)
	for _, u := range onTime {
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

	// 5. 构建响应列表
	results := make([]*dto.AttendanceDetailResponse, 0, len(records))
	for i := range records {
		record := &records[i]
		if record.Section <= 0 || record.Section > len(s.scheduleCfg.Periods) {
			continue
		}
		slotStart := s.scheduleCfg.Periods[record.Section-1].Start
		slotEnd := s.scheduleCfg.Periods[record.Section-1].End
		resp := dto.NewAttendanceDetailResponseFromRecord(record, slotStart, slotEnd, userMap)
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

	ids := make([]uint, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	return ids
}

// GetWeeklyRanking 获取本周考勤迟到排行（迟到次数从大到小）
func (s *AttendanceRecordService) GetWeeklyRanking(ctx context.Context) (*dto.WeeklyAttendanceRankingResponse, error) {
	// 1. 计算本周起止时间（周一到周日）
	now := time.Now()
	// Weekday(): Sunday=0, Monday=1...
	offset := int(time.Monday - now.Weekday())
	if offset > 0 {
		offset = -6 // 如果今天是周日(0)，则周一是6天前
	}
	// 本周一 00:00:00
	weekStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, offset)
	// 本周日 00:00:00 (查询包含周日全天的话，ListByDateRange需要支持或者我们用下周一0点作为exclude end)
	// Repository ListByDateRange 实现是 date >= ? AND date <= ?。如果date存的是日期（时间为0），则 <= weekEnd (Sunday) 是对的。
	weekEnd := weekStart.AddDate(0, 0, 6)

	// 2. 查询本周所有考勤记录
	records, err := s.attendanceRecordRepo.ListByDateRange(ctx, weekStart, weekEnd)
	if err != nil {
		return nil, errs.WrapMsgErr("获取本周考勤记录失败", err)
	}

	// 3. 统计迟到/未到次数
	lateCounts := make(map[uint]int)
	for _, r := range records {
		if r.NotArrivedIDs == "" || r.NotArrivedIDs == "[]" {
			continue
		}
		var ids []uint
		if err := json.Unmarshal([]byte(r.NotArrivedIDs), &ids); err != nil {
			s.logger.Warnw("反序列化未到人员失败", "id", r.ID, "error", err)
			continue
		}
		for _, uid := range ids {
			lateCounts[uid]++
		}
	}

	if len(lateCounts) == 0 {
		return &dto.WeeklyAttendanceRankingResponse{Items: []dto.AttendanceRankingItem{}}, nil
	}

	// 4. 获取用户信息
	userIDs := make([]uint, 0, len(lateCounts))
	for uid := range lateCounts {
		userIDs = append(userIDs, uid)
	}

	users, err := s.userRepo.ListByIDs(ctx, userIDs)
	if err != nil {
		return nil, errs.WrapMsgErr("获取用户信息失败", err)
	}

	userMap := make(map[uint]model.User)
	for _, u := range users {
		userMap[u.ID] = u
	}

	// 5. 构建结果项
	items := make([]dto.AttendanceRankingItem, 0, len(lateCounts))
	for uid, count := range lateCounts {
		user, ok := userMap[uid]
		name := "未知用户"
		avatar := ""
		if ok {
			name = user.Name
			avatar = user.Avatar
		}
		items = append(items, dto.AttendanceRankingItem{
			UserID:    uid,
			Name:      name,
			Avatar:    avatar,
			LateCount: count,
		})
	}

	// 6. 排序：迟到次数从大到小
	sort.Slice(items, func(i, j int) bool {
		return items[i].LateCount > items[j].LateCount
	})

	return &dto.WeeklyAttendanceRankingResponse{Items: items}, nil
}

// buildUserMapWithDeptFilter 构建用户映射（支持部门过滤）
// - deptIDs 为空：返回所有用户的映射
// - deptIDs 非空：只返回属于指定部门的用户
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

	// 获取指定部门的用户ID集合
	deptUsers, err := s.userRepo.ListByScope(ctx, deptIDs, nil)
	if err != nil {
		return nil, errs.WrapMsgErr("获取部门用户失败", err)
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
