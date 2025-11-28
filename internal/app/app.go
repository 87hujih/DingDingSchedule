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
)

// RunServer 启动 HTTP 服务器并支持优雅关闭
func RunServer() {
	cfg := global.AppConfig.Server

	// 初始化路由
	router := setupRouter()

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

	// 给正在处理的请求 5 秒时间完成
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		global.Log.Errorf("服务强制关闭: %v", err)
	}

	// 关闭数据库连接
	if sqlDB, err := global.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}

	global.Log.Info("服务已退出")
}
