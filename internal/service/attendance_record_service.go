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
	scheduleCfg          config.Schedule        // 配置文件作为回退
	schedulePeriodSrv    *SchedulePeriodService // 从数据库读取作息时间
	semesterSrv          *SemesterService       // 学期服务（用于判断全体应到模式）
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
	semesterSrv *SemesterService,
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
		semesterSrv:          semesterSrv,
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

	// 5. 获取应到人员
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

	// 6. 获取打卡记录（只返回正常打卡的人）
	// 【修改】传入 section 参数用于计算打卡窗口
	onTime, lateUsers, err := s.getOnTimeUsers(ctx, shouldAttend, date, req.Section, slotStart, slotEnd)
	if err != nil {
		return nil, nil, err
	}

	// 7. 获取请假人员
	leave, err := s.getLeaveUsers(ctx, shouldAttend, slotStart, slotEnd)
	if err != nil {
		return nil, nil, err
	}

	// 8. 计算未到人员（应到 - 正常打卡 - 请假）
	notArrived := s.calculateNotArrived(shouldAttend, onTime, leave)

	// 9. 构建响应
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

	return dto.NewAttendanceDetailResponseFromRecord(record, slotStart, slotEnd, userMap, userDeptNames), nil
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

	// 3. 判断是否使用"全体应到"模式（假期模式或超出学期时间）
	if s.shouldUseAllAttendMode(ctx, date) {
		// 全体应到模式：不排除有课人员
		return activeUsers, nil
	}

	// 4. 正常模式：获取该时段有课的人员
	dayOfWeek := scheduleutil.WeekdayMon1Sun7(date)
	userIDs := make([]uint, 0, len(activeUsers))
	for _, u := range activeUsers {
		userIDs = append(userIDs, u.ID)
	}

	courses, err := s.courseRepo.ListByUsersDaySection(ctx, userIDs, dayOfWeek, section)
	if err != nil {
		return nil, errs.WrapMsgErr("获取用户课表失败", err)
	}

	// 5. 过滤出本周有课的用户
	busyUserSet := make(map[uint]bool)
	for _, c := range courses {
		if weekutil.ContainsWeek(c.WeekList, week) {
			busyUserSet[c.UserID] = true
		}
	}

	// 6. 应到人员 = 候选人 - 有课人员
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
			lateCount++
			lateUsers = append(lateUsers, *user)
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

// GetAttendanceText 获取考勤文本（用于复制到群里）
// 从数据库获取已保存的考勤记录，生成简洁的文本格式
func (s *AttendanceRecordService) GetAttendanceText(
	ctx context.Context,
	req *dto.AttendanceTextRequest,
) (*dto.AttendanceTextResponse, error) {
	// 1. 从数据库获取已保存的考勤数据
	detailReq := &dto.AttendanceDetailRequest{
		Date:    req.Date,
		Week:    req.Week,
		Section: req.Section,
		DeptIDs: req.DeptIDs,
	}

	detail, err := s.GetAttendanceRecordFromDB(ctx, detailReq)
	if err != nil {
		return nil, err
	}

	// 2. 获取当前模式（假期模式或上学模式）
	mode, err := s.schedulePeriodSrv.GetCurrentMode(ctx)
	if err != nil {
		// 如果获取失败，默认使用上学模式
		mode = model.ScheduleModeSchool
	}

	// 3. 生成文本格式
	return s.formatAttendanceText(detail, mode), nil
}

// formatAttendanceText 将考勤详情格式化为文本
func (s *AttendanceRecordService) formatAttendanceText(detail *dto.AttendanceDetailResponse, mode string) *dto.AttendanceTextResponse {
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
		// 上学模式：使用第X节
		periodLabel = "第" + intToString(detail.Section) + "节"
	}

	// 构建标题
	title := "📅 " + detail.Date + " 第" + intToString(detail.Week) + "周 " + periodLabel + " 考勤"

	// 构建统计信息
	statistics := "📊 应到" + intToString(detail.Statistics.ShouldAttend) + "人，" +
		"正常打卡" + intToString(detail.Statistics.OnTime) + "人，" +
		"请假" + intToString(detail.Statistics.Leave) + "人，" +
		"未到" + intToString(detail.Statistics.NotArrived) + "人"

	// 构建分类人员列表
	content := make([]string, 0, 3)

	// 正常打卡
	if len(detail.Users.OnTime) > 0 {
		names := make([]string, 0, len(detail.Users.OnTime))
		for _, u := range detail.Users.OnTime {
			names = append(names, u.Name)
		}
		line := "✅ 正常打卡(" + intToString(len(detail.Users.OnTime)) + "人)：\n" + joinNames(names)
		content = append(content, line)
	}

	// 请假
	if len(detail.Users.Leave) > 0 {
		names := make([]string, 0, len(detail.Users.Leave))
		for _, u := range detail.Users.Leave {
			// 简洁版只显示姓名和请假类型
			nameWithType := u.Name + "（" + u.LeaveType + "）"
			names = append(names, nameWithType)
		}
		line := "🏥 请假(" + intToString(len(detail.Users.Leave)) + "人)：\n" + joinNames(names)
		content = append(content, line)
	}

	// 未到
	if len(detail.Users.NotArrived) > 0 {
		names := make([]string, 0, len(detail.Users.NotArrived))
		for _, u := range detail.Users.NotArrived {
			names = append(names, u.Name)
		}
		line := "❌ 未到(" + intToString(len(detail.Users.NotArrived)) + "人)：\n" + joinNames(names)
		content = append(content, line)
	}

	// 构建完整文本
	fullText := title + "\n" + statistics + "\n\n"
	for i, line := range content {
		fullText += line
		if i < len(content)-1 {
			fullText += "\n\n"
		}
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

// SignForUsers 代签（将指定用户从迟到列表移动到正常签到列表）
func (s *AttendanceRecordService) SignForUsers(ctx context.Context, req *dto.SignForUserRequest) (*dto.SignForUserResponse, error) {
	// 1. 获取指定考勤记录
	record, err := s.attendanceRecordRepo.FindByID(ctx, req.RecordID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.ErrNotFoundWithMsg("未找到考勤记录")
		}
		return nil, err
	}

	// 2. 解析 NotArrivedIDs 和 OnTimeIDs
	var notArrivedIDs []uint
	var onTimeUsers []dto.StoredUserCheck

	if len(record.NotArrivedIDs) > 0 {
		if err := json.Unmarshal([]byte(record.NotArrivedIDs), &notArrivedIDs); err != nil {
			s.logger.Errorw("Failed to unmarshal not_arrived_ids", "error", err)
			return nil, errs.WrapMsgErr("解析未到人员数据失败", err)
		}
	}
	if len(record.OnTimeIDs) > 0 {
		if err := json.Unmarshal([]byte(record.OnTimeIDs), &onTimeUsers); err != nil {
			s.logger.Errorw("Failed to unmarshal on_time_ids", "error", err)
			return nil, errs.WrapMsgErr("解析已签到人员数据失败", err)
		}
	}

	// 3. 处理代签
	successIDs := make([]uint, 0)
	failedIDs := make([]uint, 0)

	// 构建迟到用户Map以便快速查找
	lateMap := make(map[uint]bool)
	for _, id := range notArrivedIDs {
		lateMap[id] = true
	}

	// 构建已签到用户Map防止重复
	onTimeMap := make(map[uint]bool)
	for _, u := range onTimeUsers {
		onTimeMap[u.ID] = true
	}

	hasChange := false
	now := time.Now().Unix()

	for _, targetID := range req.TargetUserIDs {
		if lateMap[targetID] {
			// 存在于迟到列表 -> 移动
			delete(lateMap, targetID)

			// 如果不在已签到列表中，则添加
			if !onTimeMap[targetID] {
				onTimeUsers = append(onTimeUsers, dto.StoredUserCheck{
					ID:        targetID,
					CheckTime: now,
				})
				onTimeMap[targetID] = true
			}
			successIDs = append(successIDs, targetID)
			hasChange = true
		} else {
			// 不在迟到列表（可能已经签到，或本来就不需要签到）
			failedIDs = append(failedIDs, targetID)
		}
	}

	// 4. 如果有变更，更新数据库
	if hasChange {
		// 重建 notArrivedIDs slice
		newNotArrivedIDs := make([]uint, 0, len(lateMap))
		for id := range lateMap {
			newNotArrivedIDs = append(newNotArrivedIDs, id)
		}
		// 排序保持一致性 (可选，但推荐)
		sort.Slice(newNotArrivedIDs, func(i, j int) bool { return newNotArrivedIDs[i] < newNotArrivedIDs[j] })

		// 序列化
		notArrivedBytes, err := json.Marshal(newNotArrivedIDs)
		if err != nil {
			return nil, errs.WrapMsgErr("序列化未到人员数据失败", err)
		}

		onTimeBytes, err := json.Marshal(onTimeUsers)
		if err != nil {
			return nil, errs.WrapMsgErr("序列化已签到人员数据失败", err)
		}

		record.NotArrivedIDs = string(notArrivedBytes)
		record.OnTimeIDs = string(onTimeBytes)

		if err := s.attendanceRecordRepo.Upsert(ctx, record); err != nil {
			return nil, errs.WrapMsgErr("更新考勤记录失败", err)
		}
	}

	return &dto.SignForUserResponse{
		SuccessIDs: successIDs,
		FailedIDs:  failedIDs,
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
