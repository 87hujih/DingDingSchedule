package inits

import (
	"context"
	"time"

	"schedule_server/global"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"
)

// InitDepartments 初始化部门数据（从钉钉获取并写入数据库）
func InitDepartments() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deptRepo := repository.NewDepartmentRepository(global.DB)
	deptSrv := service.NewDepartmentService(deptRepo)
	if err := deptSrv.Sync(ctx); err != nil {
		global.Log.Errorf("初始化部门数据失败: %v", err)
	}
}
