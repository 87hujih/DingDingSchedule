package inits

import (
	"os"
	"path/filepath"

	"schedule_server/global"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// LogInit 初始化 zap 日志
func LogInit() {
	cfg := global.AppConfig.Log

	// 1. 创建日志目录
	logDir := filepath.Dir(cfg.Filename)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		panic("创建日志目录失败: " + err.Error())
	}

	// 2. 日志切割（Lumberjack）
	writer := &lumberjack.Logger{
		Filename:   cfg.Filename,
		MaxSize:    cfg.MaxSize,    // MB
		MaxAge:     cfg.MaxAge,     // 天
		MaxBackups: cfg.MaxBackups, // 保留文件数
		Compress:   cfg.Compress,   // 是否压缩
	}

	// 3. 解析日志级别
	level := zapcore.InfoLevel
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	// 4. 编码器配置（通用）
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:       "time",
		LevelKey:      "level",
		NameKey:       "logger",
		CallerKey:     "caller",
		FunctionKey:   zapcore.OmitKey,
		MessageKey:    "msg",
		StacktraceKey: "stack",
		LineEnding:    zapcore.DefaultLineEnding,

		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// 5. console encoder（开发环境更友好）
	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)

	// 6. file encoder（JSON，适合生产）
	fileEncoder := zapcore.NewJSONEncoder(encoderConfig)

	// 7. 日志输出目的地
	fileSyncer := zapcore.AddSync(writer)
	consoleSyncer := zapcore.AddSync(os.Stdout)

	// 8. Dev / Prod 区分输出策略
	var core zapcore.Core

	if global.AppConfig.Env == "dev" {
		// 开发环境：日志输出到控制台 + 文件
		core = zapcore.NewTee(
			zapcore.NewCore(consoleEncoder, consoleSyncer, level),
			zapcore.NewCore(fileEncoder, fileSyncer, level),
		)
	} else {
		// 生产环境：只输出 JSON 到文件
		core = zapcore.NewCore(fileEncoder, fileSyncer, level)
	}

	// 9. 构建 Logger
	logger := zap.New(
		core,
		zap.AddCaller(),
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zapcore.ErrorLevel), // 仅 Error+ 输出堆栈
	).With(
		zap.String("service", global.AppConfig.App.Name), // 添加服务名
		zap.String("env", global.AppConfig.Env),          // 添加环境信息
	)

	// 10. 存为全局 Logger
	global.Log = logger.Sugar()

	global.Log.Infow("日志初始化完成",
		"path", cfg.Filename,
		"level", cfg.Level,
		"env", global.AppConfig.Env,
	)
}
