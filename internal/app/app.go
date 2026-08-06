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
	"schedule_server/internal/agent"
	"schedule_server/internal/repository"
	"schedule_server/internal/scheduler"
	"schedule_server/internal/service"
	"schedule_server/pkg/dingtalk"

	"github.com/spf13/viper"
)

// RunServer 启动 HTTP 服务器并支持优雅关闭
func RunServer() error {
	agentRuntimeCfg, err := ParseAgentRuntimeConfig(global.AppConfig.LLM, global.AppConfig.Env)
	if err != nil {
		return fmt.Errorf("parse agent runtime config: %w", err)
	}
	runtimeAgentReadiness.configure(agentRuntimeCfg)
	logAgentRuntimeConfig(agentRuntimeCfg)

	// 初始化路由
	router := setupRouter()

	// 创建可取消的 context 用于控制 Stream 客户端生命周期
	streamCtx, streamCancel := context.WithCancel(context.Background())

	agentInstance, err := initializeAgentRuntime(agentRuntimeCfg)
	if err != nil {
		streamCancel()
		return err
	}
	if global.AppConfig.DingTalk.StreamMode {
		go startDingTalkStream(streamCtx, agentInstance)
	}
	if agentRuntimeCfg.WorkflowStore == agentWorkflowStoreDatabase {
		go monitorAgentWorkflowDatabase(streamCtx, global.DB, runtimeAgentReadiness, 10*time.Second)
	}

	// 启动考勤调度器
	attendanceScheduler := startAttendanceScheduler()

	srv := newHTTPServer(router)

	// 启动服务
	go func() {
		global.Log.Infow("服务启动", "port", global.AppConfig.Server.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			global.Log.Fatalw("服务启动失败", "error", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	global.Log.Infow("正在关闭服务...")

	// 停止钉钉 Stream 客户端
	streamCancel()

	// 给 Stream 客户端 2 秒时间优雅关闭，避免 SDK 内部 goroutine 竞态
	time.Sleep(2 * time.Second)

	// 停止 Agent（清理 session 过期 goroutine）
	if agentInstance != nil {
		agentInstance.Stop()
	}

	// 停止考勤调度器
	if attendanceScheduler != nil {
		attendanceScheduler.Stop()
	}

	// 给正在处理的请求 5 秒时间完成
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		global.Log.Errorw("服务强制关闭", "error", err)
	}

	// 关闭数据库连接
	if goAdminEng != nil {
		if errs := goAdminEng.DefaultConnection().Close(); len(errs) > 0 {
			global.Log.Warnw("关闭 GoAdmin DB 连接失败", "errors", errs)
		}
	}
	if sqlDB, err := global.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}

	global.Log.Infow("服务已退出")
	return nil
}

func newHTTPServer(handler http.Handler) *http.Server {
	cfg := global.AppConfig.Server
	readTimeout, err := time.ParseDuration(cfg.ReadTimeout)
	if err != nil {
		readTimeout = 10 * time.Second
	}
	writeTimeout, err := time.ParseDuration(cfg.WriteTimeout)
	if err != nil {
		writeTimeout = 10 * time.Second
	}
	return &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}

func initializeAgentRuntime(cfg AgentRuntimeConfig) (*agent.Agent, error) {
	if cfg.WorkflowStore == agentWorkflowStoreDatabase {
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		probeErr := probeAgentWorkflowDatabase(probeCtx, global.DB)
		probeCancel()
		if probeErr != nil {
			runtimeAgentReadiness.markWorkflowStoreUnavailable()
			return nil, fmt.Errorf("agent workflow database readiness: %w", probeErr)
		}
	}
	if !agentRuntimeMustStart(global.AppConfig.DingTalk.StreamMode, cfg) {
		return nil, nil
	}
	instance, err := initAgent()
	if err != nil {
		return nil, fmt.Errorf("initialize agent: %w", err)
	}
	return instance, nil
}

func agentRuntimeMustStart(streamMode bool, cfg AgentRuntimeConfig) bool {
	return streamMode || cfg.WorkflowStore == agentWorkflowStoreDatabase
}

func logAgentRuntimeConfig(cfg AgentRuntimeConfig) {
	if global.Log == nil {
		return
	}
	migrationDeadline := ""
	if !cfg.WorkflowMigrationDeadline.IsZero() {
		migrationDeadline = cfg.WorkflowMigrationDeadline.UTC().Format(time.RFC3339)
	}
	global.Log.Infow(
		"agent_runtime_config",
		"config_path", viper.ConfigFileUsed(),
		"environment", global.AppConfig.Env,
		"protocol_mode", cfg.ProtocolMode,
		"compiler_timeout", cfg.IntentCompilerTimeout.String(),
		"model", cfg.Model,
		"workflow_store", cfg.WorkflowStore,
		"workflow_migration", cfg.WorkflowMigration,
		"workflow_migration_deadline", migrationDeadline,
		"deterministic_compiler_mode", cfg.DeterministicCompilerMode,
		"intent_context_enabled", cfg.IntentContextEnabled,
		"log_payloads", cfg.LogPayloads,
		"fingerprint", cfg.Fingerprint(),
	)
}

// startDingTalkStream 启动钉钉 Stream 客户端（多租户模式）
func startDingTalkStream(ctx context.Context, agentInstance *agent.Agent) {
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

	// 注册 Agent 聊天消息处理器
	if agentInstance != nil {
		streamMgr.SetChatMessageHandler(runtimeAgentReadiness.wrapChat(agentInstance.Chat))

		// 构建群聊兜底推送处理器：SessionWebhook 失效时通过主动推送接口回复
		asyncReplyHandler := func(ctx context.Context, msg *dingtalk.ChatMessage, reply string) {
			tenant, dingClient, err := dingMgr.GetByCorpID(ctx, msg.CorpID)
			if err != nil {
				global.Log.Errorw("主动推送：获取钉钉客户端失败",
					"corpID", msg.CorpID,
					"err", err,
				)
				return
			}
			if err := dingClient.SendGroupRobotMessage(ctx, tenant.AppKey, msg.ConversationID, reply); err != nil {
				global.Log.Errorw("主动推送群消息失败",
					"corpID", msg.CorpID,
					"conversationID", msg.ConversationID,
					"err", err,
				)
			}
		}
		streamMgr.SetAsyncReplyHandler(asyncReplyHandler)
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

// initAgent 创建 Agent 及其依赖的 Service
func initAgent() (*agent.Agent, error) {
	repo := repository.NewRepository(global.DB)
	dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)

	schedulePeriodSrv := service.NewSchedulePeriodService(
		repo.SchedulePeriodRepo,
		repo.ScheduleSettingRepo,
		&global.AppConfig.Schedule,
	)
	semesterSrv := service.NewSemesterService(repo.SemesterRepo)

	scheduleSrv := service.NewScheduleService(
		repo.CourseRepo,
		repo.UserRepo,
		repo.SemesterRepo,
		repo.ScheduleSettingRepo,
		dingMgr,
		global.Log,
	)
	attendanceSrv := service.NewAttendanceRecordService(
		repo.UserRepo,
		repo.CourseRepo,
		repo.LeaveRepo,
		repo.AttendanceRecordRepo,
		repo.AttendanceManualOverrideRepo,
		repo.ScheduleSettingRepo,
		repo.UserRestDayRepo,
		dingMgr,
		schedulePeriodSrv,
		semesterSrv,
		global.AppConfig.Schedule,
		global.Log,
	)
	restDaySrv := service.NewRestDayService(
		repo.UserRestDayRepo,
		repo.ScheduleSettingRepo,
		repo.UserRepo,
		global.Log,
	)
	leaveSyncSrv := service.NewLeaveSyncService(repo.LeaveRepo, repo.UserRepo, dingMgr, global.Log)
	knowledgeSrv := service.NewAgentKnowledgeService(repo.AgentKnowledgeRepo, global.Log)

	return BuildAgent(repo, scheduleSrv, attendanceSrv, semesterSrv, schedulePeriodSrv, restDaySrv, leaveSyncSrv, knowledgeSrv, nil)
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
		repo.AttendanceManualOverrideRepo,
		repo.ScheduleSettingRepo,
		repo.UserRestDayRepo,
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
		repo.GroupSubRepo,
		dingMgr,
		global.Log,
	)

	// 启动调度器
	attendanceScheduler.Start()

	return attendanceScheduler
}
