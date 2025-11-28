package inits

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
}
