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
// 使用方法: go run ./scripts/migrate_schedule_periods
func main() {
	inits.ConfigInit()
	inits.LogInit()
	inits.DBInit()
	inits.AutoMigrate()

	var tenants []model.Tenant
	if err := global.DB.Find(&tenants).Error; err != nil {
		log.Fatal("查询租户失败:", err)
	}

	if len(tenants) == 0 {
		fmt.Println("没有找到租户，请先创建租户")
		return
	}

	for _, tenant := range tenants {
		ctx := tenantctx.WithSkipTenantScope(context.Background())

		var count int64
		global.DB.WithContext(ctx).Model(&model.SchedulePeriod{}).
			Where("tenant_id = ?", tenant.ID).
			Count(&count)

		if count > 0 {
			fmt.Printf("租户 %s (ID=%d) 已有配置，跳过\n", tenant.Name, tenant.ID)
			continue
		}

		for i, p := range global.AppConfig.Schedule.Periods {
			period := &model.SchedulePeriod{
				TenantID:  tenant.ID,
				Name:      p.Name,
				StartTime: p.Start + ":00",
				EndTime:   p.End + ":00",
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
