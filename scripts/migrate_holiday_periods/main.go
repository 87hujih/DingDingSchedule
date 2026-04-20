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

// 为现有租户添加假期作息配置
// 使用方法: go run ./scripts/migrate_holiday_periods
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
		fmt.Println("没有找到租户")
		return
	}

	holidayPeriods := []struct {
		Name      string
		StartTime string
		EndTime   string
		SortOrder int
	}{
		{"上午", "08:30:00", "12:00:00", 1},
		{"下午", "14:00:00", "18:00:00", 2},
		{"晚上", "19:00:00", "21:00:00", 3},
	}

	for _, tenant := range tenants {
		ctx := tenantctx.WithSkipTenantScope(context.Background())

		global.DB.WithContext(ctx).Model(&model.SchedulePeriod{}).
			Where("tenant_id = ? AND (mode = '' OR mode IS NULL)", tenant.ID).
			Update("mode", model.ScheduleModeSchool)

		var count int64
		global.DB.WithContext(ctx).Model(&model.SchedulePeriod{}).
			Where("tenant_id = ? AND mode = ?", tenant.ID, model.ScheduleModeHoliday).
			Count(&count)

		if count > 0 {
			fmt.Printf("租户 %s (ID=%d) 已有假期配置，跳过\n", tenant.Name, tenant.ID)
		} else {
			for _, p := range holidayPeriods {
				period := &model.SchedulePeriod{
					TenantID:  tenant.ID,
					Mode:      model.ScheduleModeHoliday,
					Name:      p.Name,
					StartTime: p.StartTime,
					EndTime:   p.EndTime,
					SortOrder: p.SortOrder,
					IsActive:  true,
				}
				if err := global.DB.WithContext(ctx).Create(period).Error; err != nil {
					log.Printf("创建假期配置失败 (租户=%d): %v\n", tenant.ID, err)
					continue
				}
			}
			fmt.Printf("租户 %s (ID=%d) 假期配置添加完成\n", tenant.Name, tenant.ID)
		}

		var setting model.ScheduleSetting
		result := global.DB.WithContext(ctx).Where("tenant_id = ?", tenant.ID).First(&setting)
		if result.Error != nil {
			setting = model.ScheduleSetting{
				TenantID:                    tenant.ID,
				CurrentMode:                 model.ScheduleModeSchool,
				AttendanceEnabled:           true,
				ScheduleChangeNotifyEnabled: true,
				LateNotifyEnabled:           true,
				RestDayEditingAllowed:       true,
				RestDayAttendanceEnabled:    true,
			}
			if err := global.DB.WithContext(ctx).Create(&setting).Error; err != nil {
				log.Printf("创建作息设置失败 (租户=%d): %v\n", tenant.ID, err)
			} else {
				fmt.Printf("租户 %s (ID=%d) 作息设置初始化完成\n", tenant.Name, tenant.ID)
			}
		}
	}

	fmt.Println("迁移完成！")
}
