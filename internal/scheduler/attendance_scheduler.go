package scheduler

import (
	"context"
	"fmt"
	"time"

	"schedule_server/config"
	"schedule_server/internal/dto"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"
	"schedule_server/internal/tenantctx"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

// AttendanceScheduler 考勤定时调度器
//
// 功能说明：
// - 支持多租户独立配置
// - 支持动态配置重载（每10秒检查一次）
// - 支持配置回退（数据库 → YAML）
// - 在每节课上课后延迟 delayAfterClassStart 分钟触发统计
type AttendanceScheduler struct {
	scheduleCfg          config.Schedule
	tenantRepo           repository.TenantRepository
	schedulePeriodRepo   repository.SchedulePeriodRepository
	scheduleSettingRepo  repository.ScheduleSettingRepository
	attendanceRecordSrv  *service.AttendanceRecordService
	semesterSrv          *service.SemesterService
	logger               *zap.SugaredLogger
	cron                 *cron.Cron
	delayAfterClassStart int // 上课后延迟多少分钟统计，默认0分钟（立即执行）

	// tenantJobs 存储每个租户的调度任务ID
	tenantJobs map[uint][]cron.EntryID
}

// NewAttendanceScheduler 创建考勤调度器
func NewAttendanceScheduler(
	scheduleCfg config.Schedule,
	tenantRepo repository.TenantRepository,
	schedulePeriodRepo repository.SchedulePeriodRepository,
	scheduleSettingRepo repository.ScheduleSettingRepository,
	attendanceRecordSrv *service.AttendanceRecordService,
	semesterSrv *service.SemesterService,
	logger *zap.SugaredLogger,
) *AttendanceScheduler {
	return &AttendanceScheduler{
		scheduleCfg:          scheduleCfg,
		tenantRepo:           tenantRepo,
		schedulePeriodRepo:   schedulePeriodRepo,
		scheduleSettingRepo:  scheduleSettingRepo,
		attendanceRecordSrv:  attendanceRecordSrv,
		semesterSrv:          semesterSrv,
		logger:               logger,
		delayAfterClassStart: 0,
		tenantJobs:           make(map[uint][]cron.EntryID),
	}
}

// Start 启动调度器
func (s *AttendanceScheduler) Start() {
	s.logger.Info("考勤调度器启动")

	// 创建 cron 实例，使用本地时区，支持秒级调度
	s.cron = cron.New(cron.WithSeconds(), cron.WithLocation(time.Local))

	// 初始加载所有租户的调度任务
	s.reloadAllTenantSchedules()

	// 添加定时重载任务（每10秒检查一次配置变更）
	_, err := s.cron.AddFunc("*/10 * * * * *", func() {
		s.reloadAllTenantSchedules()
	})
	if err != nil {
		s.logger.Errorw("添加配置重载任务失败", "err", err)
	}

	// 启动 cron
	s.cron.Start()
	s.logger.Infof("考勤调度器已启动，共 %d 个定时任务", len(s.cron.Entries()))
}

// Stop 停止调度器
func (s *AttendanceScheduler) Stop() {
	s.logger.Info("正在停止考勤调度器...")
	if s.cron != nil {
		ctx := s.cron.Stop()
		<-ctx.Done()
	}
	s.logger.Info("考勤调度器已停止")
}

// ReloadTenant 手动触发单个租户的配置重载
func (s *AttendanceScheduler) ReloadTenant(tenantID uint) {
	s.reloadTenantSchedule(tenantID)
}

// reloadAllTenantSchedules 重新加载所有租户的调度配置
func (s *AttendanceScheduler) reloadAllTenantSchedules() {
	ctx := context.Background()

	// 获取所有活跃租户
	tenants, err := s.tenantRepo.ListActive(ctx)
	if err != nil {
		s.logger.Errorw("获取活跃租户失败", "err", err)
		return
	}

	if len(tenants) == 0 {
		return
	}

	// 为每个租户重载调度任务
	for _, tenant := range tenants {
		s.reloadTenantSchedule(tenant.ID)
	}
}

// reloadTenantSchedule 重新加载单个租户的调度配置
func (s *AttendanceScheduler) reloadTenantSchedule(tenantID uint) {
	ctx := tenantctx.WithTenantID(context.Background(), tenantID)

	// 1. 获取租户的作息时间配置（自动根据当前模式）
	periods, err := s.schedulePeriodRepo.ListActive(ctx)
	if err != nil {
		s.logger.Warnw("获取租户作息配置失败，使用配置文件回退",
			"tenantId", tenantID,
			"err", err,
		)
		s.loadTenantScheduleFromConfig(tenantID)
		return
	}

	if len(periods) == 0 {
		s.logger.Warnw("租户无作息配置，使用配置文件回退", "tenantId", tenantID)
		s.loadTenantScheduleFromConfig(tenantID)
		return
	}

	// 2. 移除该租户的旧任务
	s.removeTenantJobs(tenantID)

	// 3. 为每个作息时段创建新任务
	var newEntryIDs []cron.EntryID
	for i, period := range periods {
		section := i + 1
		cronExpr, err := s.buildCronExpressionFromTime(period.StartTime)
		if err != nil {
			s.logger.Warnw("构建cron表达式失败",
				"tenantId", tenantID,
				"section", section,
				"periodName", period.Name,
				"startTime", period.StartTime,
				"err", err,
			)
			continue
		}

		// 闭包捕获变量
		tid := tenantID
		sec := section
		prd := period

		entryID, err := s.cron.AddFunc(cronExpr, func() {
			s.triggerAttendanceForTenant(tid, sec, time.Now())
		})

		if err != nil {
			s.logger.Errorw("添加定时任务失败",
				"tenantId", tenantID,
				"section", section,
				"cronExpr", cronExpr,
				"err", err,
			)
			continue
		}

		newEntryIDs = append(newEntryIDs, entryID)
	}

	// 4. 保存新任务ID
	s.tenantJobs[tenantID] = newEntryIDs
}

// loadTenantScheduleFromConfig 从配置文件加载租户调度（回退方案）
func (s *AttendanceScheduler) loadTenantScheduleFromConfig(tenantID uint) {
	if len(s.scheduleCfg.Periods) == 0 {
		s.logger.Warnw("配置文件中无作息配置", "tenantId", tenantID)
		return
	}

	// 移除旧任务
	s.removeTenantJobs(tenantID)

	var newEntryIDs []cron.EntryID
	for i, period := range s.scheduleCfg.Periods {
		section := i + 1
		cronExpr, err := s.buildCronExpression(period.Start)
		if err != nil {
			s.logger.Warnw("构建cron表达式失败",
				"tenantId", tenantID,
				"section", section,
				"periodName", period.Name,
				"startTime", period.Start,
				"err", err,
			)
			continue
		}

		tid := tenantID
		sec := section
		prd := period

		entryID, err := s.cron.AddFunc(cronExpr, func() {
			s.triggerAttendanceForTenant(tid, sec, time.Now())
		})

		if err != nil {
			s.logger.Errorw("添加定时任务失败",
				"tenantId", tenantID,
				"section", section,
				"err", err,
			)
			continue
		}

		newEntryIDs = append(newEntryIDs, entryID)
	}

	s.tenantJobs[tenantID] = newEntryIDs
}

// removeTenantJobs 移除租户的所有调度任务
func (s *AttendanceScheduler) removeTenantJobs(tenantID uint) {
	if entryIDs, exists := s.tenantJobs[tenantID]; exists {
		for _, entryID := range entryIDs {
			s.cron.Remove(entryID)
		}
		delete(s.tenantJobs, tenantID)
	}
}

// buildCronExpressionFromTime 从数据库时间格式构建cron表达式
// 输入: "08:00:00" 或 "08:00"
// 输出: "0 3 8 * * *" (每天8:03)
func (s *AttendanceScheduler) buildCronExpressionFromTime(timeStr string) (string, error) {
	// 支持 "HH:MM:SS" 和 "HH:MM" 格式
	var startClock time.Time
	var err error

	if len(timeStr) == 8 { // "08:00:00"
		startClock, err = time.Parse("15:04:05", timeStr)
	} else if len(timeStr) == 5 { // "08:00"
		startClock, err = time.Parse("15:04", timeStr)
	} else {
		return "", fmt.Errorf("无效的时间格式: %s", timeStr)
	}

	if err != nil {
		return "", fmt.Errorf("解析时间失败: %w", err)
	}

	// 计算触发时间 = 上课时间 + 延迟分钟数
	triggerTime := startClock.Add(time.Duration(s.delayAfterClassStart) * time.Minute)

	// cron表达式格式: 秒 分 时 日 月 周
	cronExpr := fmt.Sprintf("%d %d %d * * *", triggerTime.Second(), triggerTime.Minute(), triggerTime.Hour())

	return cronExpr, nil
}

// buildCronExpression 从YAML配置时间格式构建cron表达式（用于配置文件回退）
// 输入: "08:00"
// 输出: "0 3 8 * * *" (每天8:03)
func (s *AttendanceScheduler) buildCronExpression(startTimeStr string) (string, error) {
	startClock, err := time.Parse("15:04", startTimeStr)
	if err != nil {
		return "", fmt.Errorf("解析上课时间失败: %w", err)
	}

	triggerTime := startClock.Add(time.Duration(s.delayAfterClassStart) * time.Minute)
	cronExpr := fmt.Sprintf("0 %d %d * * *", triggerTime.Minute(), triggerTime.Hour())

	return cronExpr, nil
}

// triggerAttendanceForTenant 触发单个租户的考勤统计
func (s *AttendanceScheduler) triggerAttendanceForTenant(tenantID uint, section int, now time.Time) {
	ctx := tenantctx.WithTenantID(context.Background(), tenantID)

	// 检查考勤全局开关
	enabled, err := s.scheduleSettingRepo.IsAttendanceEnabled(ctx)
	if err != nil {
		s.logger.Warnw("检查考勤开关失败", "tenantId", tenantID, "err", err)
		// 检查失败时继续执行（保守策略）
	}
	if !enabled {
		return
	}

	// 获取租户信息（用于日志）
	tenant, err := s.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		s.logger.Errorw("获取租户信息失败", "tenantId", tenantID, "err", err)
		return
	}

	// 获取当前模式
	setting, _ := s.scheduleSettingRepo.GetByTenantID(ctx)
	currentMode := "school"
	if setting != nil {
		currentMode = setting.CurrentMode
	}

	// 获取当前周数
	week, err := s.semesterSrv.GetCurrentWeek(ctx)
	if err != nil {
		// 假期模式下，不受学期配置限制，使用周数 0 继续执行
		if currentMode == "holiday" {
			week = 0
		} else {
			s.logger.Warnw("获取当前周数失败",
				"tenantId", tenantID,
				"tenantName", tenant.Name,
				"err", err,
			)
			return
		}
	}

	date := now.Format("2006-01-02")

	// 构建请求
	req := &dto.AttendanceDetailRequest{
		Date:    date,
		Week:    week,
		Section: section,
	}

	// 获取考勤详情
	result, lateUsers, err := s.attendanceRecordSrv.GetAttendanceDetailWithLateUsers(ctx, req)
	if err != nil {
		s.logger.Errorw("获取考勤详情失败",
			"tenantId", tenantID,
			"err", err,
		)
		return
	}

	// 保存到数据库
	if err := s.attendanceRecordSrv.SaveAttendanceRecord(ctx, result); err != nil {
		s.logger.Errorw("保存考勤记录失败",
			"tenantId", tenantID,
			"err", err,
		)
		return
	}

	if err := s.attendanceRecordSrv.SendLateNotifications(ctx, result.Date, result.Section, result.SlotTime.Start, result.SlotTime.End, currentMode, lateUsers); err != nil {
		s.logger.Errorw("发送迟到提醒失败",
			"tenantId", tenantID,
			"err", err,
		)
	}

	s.logger.Infow("考勤统计完成",
		"tenantId", tenantID,
		"tenantName", tenant.Name,
		"shouldAttend", result.Statistics.ShouldAttend,
		"onTime", result.Statistics.OnTime,
		"leave", result.Statistics.Leave,
		"notArrived", result.Statistics.NotArrived,
	)
}
