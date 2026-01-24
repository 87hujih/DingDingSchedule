package main

import (
	"context"
	"fmt"
	"log"

	"schedule_server/global"
	"schedule_server/inits"
	"schedule_server/internal/model"
	"schedule_server/internal/tenantctx"
)

// 将配置文件中的作息时间迁移到数据库
// 使用方法: go run scripts/migrate_schedule_periods.go
func main() {
	// 初始化配置
	inits.ConfigInit()

	// 初始化日志
	inits.LogInit()

	// 初始化数据库
	inits.DBInit()

	// 执行数据库迁移（创建表）
	inits.AutoMigrate()

	// 获取所有租户
	var tenants []model.Tenant
	if err := global.DB.Find(&tenants).Error; err != nil {
		log.Fatal("查询租户失败:", err)
	}

	if len(tenants) == 0 {
		fmt.Println("没有找到租户，请先创建租户")
		return
	}

	// 为每个租户创建作息时间配置
	for _, tenant := range tenants {
		ctx := tenantctx.WithSkipTenantScope(context.Background())

		// 检查是否已有配置
		var count int64
		global.DB.WithContext(ctx).Model(&model.SchedulePeriod{}).
			Where("tenant_id = ?", tenant.ID).
			Count(&count)

		if count > 0 {
			fmt.Printf("租户 %s (ID=%d) 已有配置，跳过\n", tenant.Name, tenant.ID)
			continue
		}

		// 从配置文件读取并插入
		for i, p := range global.AppConfig.Schedule.Periods {
			period := &model.SchedulePeriod{
				TenantID:  tenant.ID,
				Name:      p.Name,
				StartTime: p.Start + ":00", // "08:00" -> "08:00:00"
				EndTime:   p.End + ":00",   // "09:40" -> "09:40:00"
				SortOrder: i + 1,
				IsActive:  true,
			}

			if err := global.DB.WithContext(ctx).Create(period).Error; err != nil {
				log.Printf("创建配置失败 (租户=%d): %v\n", tenant.ID, err)
				continue
			}
		}

		fmt.Printf("租户 %s (ID=%d) 迁移完成，共 %d 条配置\n",
			tenant.Name, tenant.ID, len(global.AppConfig.Schedule.Periods))
	}

	fmt.Println("迁移完成！")
}
