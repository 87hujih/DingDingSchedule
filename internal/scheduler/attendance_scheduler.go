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
// 统计时机说明：
// - 在每节课上课后延迟 delayAfterClassStart 分钟触发统计
// - 统计时在上课时间前打卡的人标记为正常打卡(on_time)
// - 未打卡且未请假的人标记为未到(not_arrived)
type AttendanceScheduler struct {
	scheduleCfg          config.Schedule
	tenantRepo           repository.TenantRepository
	attendanceRecordSrv  *service.AttendanceRecordService
	semesterSrv          *service.SemesterService
	logger               *zap.SugaredLogger
	cron                 *cron.Cron
	delayAfterClassStart int // 上课后延迟多少分钟统计，默认3分钟
}

// NewAttendanceScheduler 创建考勤调度器
func NewAttendanceScheduler(
	scheduleCfg config.Schedule,
	tenantRepo repository.TenantRepository,
	attendanceRecordSrv *service.AttendanceRecordService,
	semesterSrv *service.SemesterService,
	logger *zap.SugaredLogger,
) *AttendanceScheduler {
	return &AttendanceScheduler{
		scheduleCfg:          scheduleCfg,
		tenantRepo:           tenantRepo,
		attendanceRecordSrv:  attendanceRecordSrv,
		semesterSrv:          semesterSrv,
		logger:               logger,
		delayAfterClassStart: 3,
	}
}

// Start 启动调度器
func (s *AttendanceScheduler) Start() {
	s.logger.Info("考勤调度器启动")

	// 创建 cron 实例，使用本地时区
	s.cron = cron.New(cron.WithLocation(time.Local))

	// 为每节课添加定时任务
	for i, period := range s.scheduleCfg.Periods {
		section := i + 1
		cronExpr, err := s.buildCronExpression(period.Start)
		if err != nil {
			s.logger.Warnw("构建 cron 表达式失败",
				"section", section,
				"periodName", period.Name,
				"startTime", period.Start,
				"err", err,
			)
			continue
		}

		// 闭包捕获 section 和 period
		sec := section
		prd := period
		_, err = s.cron.AddFunc(cronExpr, func() {
			s.logger.Infow("触发考勤统计",
				"section", sec,
				"periodName", prd.Name,
				"cronExpr", cronExpr,
			)
			s.triggerAttendanceStatistics(sec, time.Now())
		})
		if err != nil {
			s.logger.Errorw("添加定时任务失败",
				"section", section,
				"cronExpr", cronExpr,
				"err", err,
			)
			continue
		}

		s.logger.Infow("添加考勤定时任务",
			"section", section,
			"periodName", period.Name,
			"startTime", period.Start,
			"cronExpr", cronExpr,
		)
	}

	// 启动 cron
	s.cron.Start()
	s.logger.Infof("考勤调度器已启动，共 %d 个定时任务", len(s.cron.Entries()))
}

// Stop 停止调度器
func (s *AttendanceScheduler) Stop() {
	s.logger.Info("正在停止考勤调度器...")
	if s.cron != nil {
		ctx := s.cron.Stop() // 返回一个 context，等待所有正在运行的任务完成
		<-ctx.Done()
	}
	s.logger.Info("考勤调度器已停止")
}

// buildCronExpression 构建 cron 表达式
// 输入: 上课时间 "08:00"
// 输出: "3 8 * * *" （每天 8:03）
func (s *AttendanceScheduler) buildCronExpression(startTimeStr string) (string, error) {
	startClock, err := time.Parse("15:04", startTimeStr)
	if err != nil {
		return "", fmt.Errorf("解析上课时间失败: %w", err)
	}

	// 计算触发时间 = 上课时间 + 延迟分钟数
	triggerTime := startClock.Add(time.Duration(s.delayAfterClassStart) * time.Minute)

	// cron 表达式格式: 分 时 日 月 周
	// * 表示每天（周一到周日）
	cronExpr := fmt.Sprintf("%d %d * * *", triggerTime.Minute(), triggerTime.Hour())

	return cronExpr, nil
}

// triggerAttendanceStatistics 触发考勤统计
func (s *AttendanceScheduler) triggerAttendanceStatistics(section int, now time.Time) {
	ctx := context.Background()

	// 获取所有活跃租户
	tenants, err := s.tenantRepo.ListActive(ctx)
	if err != nil {
		s.logger.Errorw("获取活跃租户失败", "err", err)
		return
	}

	if len(tenants) == 0 {
		s.logger.Warn("没有活跃的租户，跳过考勤统计")
		return
	}

	date := now.Format("2006-01-02")

	for _, tenant := range tenants {
		// 为每个租户创建带租户上下文的 context
		tenantCtx := tenantctx.WithTenantID(ctx, tenant.ID)

		// 获取当前周数
		week, err := s.semesterSrv.GetCurrentWeek(tenantCtx)
		if err != nil {
			s.logger.Warnw("获取当前周数失败",
				"tenantId", tenant.ID,
				"tenantName", tenant.Name,
				"err", err,
			)
			continue
		}

		// 构建请求
		req := &dto.AttendanceDetailRequest{
			Date:    date,
			Week:    week,
			Section: section,
		}

		s.logger.Infow("开始统计考勤",
			"tenantId", tenant.ID,
			"tenantName", tenant.Name,
			"date", date,
			"week", week,
			"section", section,
		)

		// 获取考勤详情
		result, err := s.attendanceRecordSrv.GetAttendanceDetail(tenantCtx, req)
		if err != nil {
			s.logger.Errorw("获取考勤详情失败",
				"tenantId", tenant.ID,
				"err", err,
			)
			continue
		}

		// 保存到数据库
		if err := s.attendanceRecordSrv.SaveAttendanceRecord(tenantCtx, result); err != nil {
			s.logger.Errorw("保存考勤记录失败",
				"tenantId", tenant.ID,
				"err", err,
			)
			continue
		}

		s.logger.Infow("考勤统计完成",
			"tenantId", tenant.ID,
			"tenantName", tenant.Name,
			"shouldAttend", result.Statistics.ShouldAttend,
			"onTime", result.Statistics.OnTime,
			"leave", result.Statistics.Leave,
			"notArrived", result.Statistics.NotArrived,
		)
	}
}
