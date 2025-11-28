package global

import (
	"schedule_server/config"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	// AppConfig 总配置对象
	AppConfig config.Config
	// DB MySQL 连接
	DB *gorm.DB
	// Log 日志对象
	Log *zap.SugaredLogger
)
