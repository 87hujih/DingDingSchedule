package inits

import (
	"schedule_server/global"
	"schedule_server/internal/repository"
)

// Init 统一初始化入口，按顺序初始化所有组件
func Init() {
	// 初始化配置
	ConfigInit()

	// 初始化日志
	LogInit()

	// 初始化 MySQL
	DBInit()

	// 初始化表
	AutoMigrate()

	// 启用 GORM 租户隔离插件（必须在 AutoMigrate 之后，避免迁移阶段缺少 tenant ctx）
	if err := global.DB.Use(repository.NewTenantScopePlugin()); err != nil {
		global.Log.Fatalf("初始化租户隔离插件失败: %v", err)
	}

	// 激活 Error+ 日志入库（必须在 AutoMigrate 和租户插件初始化之后）
	AttachDBToLogger(global.DB)
}
