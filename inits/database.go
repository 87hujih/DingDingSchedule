package inits

import (
	"time"

	"schedule_server/global"
	"schedule_server/internal/model"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DBInit 初始化 MySQL 数据库连接
func DBInit() {
	cfg := global.AppConfig.Database

	db, err := gorm.Open(mysql.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		global.Log.Fatalf("连接数据库失败: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		global.Log.Fatalf("获取 sql.DB 失败: %v", err)
	}

	// 连接池配置
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)

	// 解析 conn_max_lifetime
	if lifetime, err := time.ParseDuration(cfg.ConnMaxLifetime); err == nil {
		sqlDB.SetConnMaxLifetime(lifetime)
	} else {
		sqlDB.SetConnMaxLifetime(time.Hour) // 默认 1 小时
	}

	global.DB = db
	global.Log.Info("数据库连接成功")
}

// AutoMigrate 自动化迁移表
func AutoMigrate() {
	if err := global.DB.AutoMigrate(
		&model.Tenant{},
		&model.User{},
		&model.UserType{},
		&model.Department{},
		&model.UserDepartment{},
		&model.Course{},
		&model.LeaveApproval{},
		&model.Semester{},
		&model.SchedulePeriod{},
		&model.ScheduleSetting{},
		&model.AttendanceRecord{},
		&model.AttendanceManualOverride{},
		&model.AuditLog{},
		&model.UserRestDay{},
		&model.SystemLog{},
		&model.GroupAttendanceSubscription{},
		&model.AgentCallLog{},
	); err != nil {
		global.Log.Fatalf("数据库迁移失败: %v", err)
	}
}
