package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"schedule_server/global"
	"schedule_server/internal/repository"
	"schedule_server/internal/scheduler"
	"schedule_server/internal/service"
	"schedule_server/pkg/dingtalk"
)

// RunServer 启动 HTTP 服务器并支持优雅关闭
func RunServer() {
	cfg := global.AppConfig.Server

	// 初始化路由
	router := setupRouter()

	// 创建可取消的 context 用于控制 Stream 客户端生命周期
	streamCtx, streamCancel := context.WithCancel(context.Background())

	// 启动钉钉 Stream 客户端（如果启用）
	if global.AppConfig.DingTalk.StreamMode {
		go startDingTalkStream(streamCtx)
	}

	// 启动考勤调度器
	attendanceScheduler := startAttendanceScheduler()

	// 解析超时配置
	readTimeout, err := time.ParseDuration(cfg.ReadTimeout)
	if err != nil {
		readTimeout = 10 * time.Second
	}
	writeTimeout, err := time.ParseDuration(cfg.WriteTimeout)
	if err != nil {
		writeTimeout = 10 * time.Second
	}

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	// 启动服务
	go func() {
		global.Log.Infof("服务启动，端口: %d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			global.Log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	global.Log.Info("正在关闭服务...")

	// 停止钉钉 Stream 客户端
	streamCancel()
	global.Log.Info("已发送 Stream 客户端关闭信号")

	// 停止考勤调度器
	if attendanceScheduler != nil {
		attendanceScheduler.Stop()
	}

	// 给正在处理的请求 5 秒时间完成
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		global.Log.Errorf("服务强制关闭: %v", err)
	}

	// 关闭数据库连接
	if goAdminEng != nil {
		if errs := goAdminEng.DefaultConnection().Close(); len(errs) > 0 {
			global.Log.Warnf("关闭 GoAdmin DB 连接失败: %v", errs)
		}
	}
	if sqlDB, err := global.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}

	global.Log.Info("服务已退出")
}

// startDingTalkStream 启动钉钉 Stream 客户端
func startDingTalkStream(ctx context.Context) {
	cfg := global.AppConfig.DingTalk
	if cfg.AppKey == "" || cfg.AppSecret == "" {
		global.Log.Warn("钉钉 Stream 模式未配置 AppKey/AppSecret，跳过启动")
		return
	}

	// 创建依赖
	repo := repository.NewRepository(global.DB)
	dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)
	leaveSyncSrv := service.NewLeaveSyncService(repo.LeaveRepo, repo.UserRepo, dingMgr, global.Log)

	// 创建 Stream 客户端
	streamClient := dingtalk.NewStreamClient(cfg.AppKey, cfg.AppSecret, cfg.CorpID, global.Log)

	// 设置审批事件处理器
	streamClient.SetBpmsEventHandler(func(ctx context.Context, corpID, processInstanceID, eventType string) error {
		global.Log.Infow("处理审批事件",
			"corpId", corpID,
			"processInstanceId", processInstanceID,
			"eventType", eventType,
		)
		return leaveSyncSrv.SyncProcessInstance(ctx, corpID, processInstanceID)
	})

	// 启动（阻塞，使用传入的可取消 context）
	if err := streamClient.Start(ctx); err != nil && err != context.Canceled {
		global.Log.Errorw("钉钉 Stream 客户端启动失败", "err", err)
	}
}

// startAttendanceScheduler 启动考勤调度器
func startAttendanceScheduler() *scheduler.AttendanceScheduler {
	// 创建依赖
	repo := repository.NewRepository(global.DB)
	dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)

	schedulePeriodSrv := service.NewSchedulePeriodService(repo.SchedulePeriodRepo, repo.ScheduleSettingRepo, &global.AppConfig.Schedule)

	attendanceRecordSrv := service.NewAttendanceRecordService(
		repo.UserRepo,
		repo.CourseRepo,
		repo.LeaveRepo,
		repo.AttendanceRecordRepo,
		dingMgr,
		schedulePeriodSrv,
		global.AppConfig.Schedule,
		global.Log,
	)
	semesterSrv := service.NewSemesterService(repo.SemesterRepo)

	// 创建调度器（支持多租户动态配置）
	attendanceScheduler := scheduler.NewAttendanceScheduler(
		global.AppConfig.Schedule,
		repo.TenantRepo,
		repo.SchedulePeriodRepo,
		repo.ScheduleSettingRepo,
		attendanceRecordSrv,
		semesterSrv,
		global.Log,
	)

	// 启动调度器
	attendanceScheduler.Start()

	return attendanceScheduler
}
