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

	// 给 Stream 客户端 2 秒时间优雅关闭，避免 SDK 内部 goroutine 竞态
	time.Sleep(2 * time.Second)

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

// startDingTalkStream 启动钉钉 Stream 客户端（多租户模式）
func startDingTalkStream(ctx context.Context) {
	// 捕获 SDK 内部 goroutine 可能的 panic（如关闭时的 "send on closed channel"）
	defer func() {
		if r := recover(); r != nil {
			global.Log.Errorw("钉钉 Stream 客户端 goroutine panic 已捕获", "panic", r)
		}
	}()

	// 创建依赖
	repo := repository.NewRepository(global.DB)
	dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)
	leaveSyncSrv := service.NewLeaveSyncService(repo.LeaveRepo, repo.UserRepo, dingMgr, global.Log)

	// 创建多租户 Stream 客户端管理器
	streamMgr := service.NewStreamClientManager(repo.TenantRepo, global.Log)

	// 定义事件处理器
	eventHandler := func(ctx context.Context, corpID, processInstanceID, eventType string) error {
		return leaveSyncSrv.SyncProcessInstance(ctx, corpID, processInstanceID)
	}

	// 启动所有活跃租户的 Stream 客户端
	if err := streamMgr.StartAll(ctx, eventHandler); err != nil {
		global.Log.Errorw("启动 Stream 客户端管理器失败", "err", err)
		return
	}

	// 等待 context 取消
	<-ctx.Done()

	// 停止所有客户端
	streamMgr.StopAll()
}

// startAttendanceScheduler 启动考勤调度器
func startAttendanceScheduler() *scheduler.AttendanceScheduler {
	// 创建依赖
	repo := repository.NewRepository(global.DB)
	dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)

	// 创建 SchedulePeriodService
	schedulePeriodSrv := service.NewSchedulePeriodService(
		repo.SchedulePeriodRepo,
		repo.ScheduleSettingRepo,
		&global.AppConfig.Schedule,
	)

	// 创建 SemesterService
	semesterSrv := service.NewSemesterService(repo.SemesterRepo)

	// 创建 AttendanceRecordService，注入 SchedulePeriodService 和 SemesterService
	attendanceRecordSrv := service.NewAttendanceRecordService(
		repo.UserRepo,
		repo.CourseRepo,
		repo.LeaveRepo,
		repo.AttendanceRecordRepo,
		dingMgr,
		schedulePeriodSrv,
		semesterSrv,
		global.AppConfig.Schedule,
		global.Log,
	)

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
