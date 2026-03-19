package service

import (
	"context"
	"slices"
	"testing"

	"schedule_server/config"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetFreeUsersBySlotFiltersByDeptID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:schedule-service-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(&model.User{}, &model.Department{}, &model.UserDepartment{}, &model.Course{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	const tenantID = uint(1)
	const deptA = int64(101)
	const deptB = int64(102)

	if err := db.Create(&[]model.Department{
		{TenantID: tenantID, DeptID: deptA, Name: "学生会", IsLeaf: true, Status: 1},
		{TenantID: tenantID, DeptID: deptB, Name: "办公室", IsLeaf: true, Status: 1},
	}).Error; err != nil {
		t.Fatalf("create departments: %v", err)
	}

	users := []model.User{
		{TenantID: tenantID, DingUserID: "u-a", Name: "张三", Status: 1},
		{TenantID: tenantID, DingUserID: "u-b", Name: "李四", Status: 1},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}

	if err := db.Create(&[]model.UserDepartment{
		{TenantID: tenantID, UserID: users[0].ID, DeptID: deptA},
		{TenantID: tenantID, UserID: users[1].ID, DeptID: deptB},
	}).Error; err != nil {
		t.Fatalf("create user departments: %v", err)
	}

	svc := &ScheduleService{
		userRepo:   repository.NewUserRepository(db),
		courseRepo: repository.NewCourseRepository(db),
		logger:     zap.NewNop().Sugar(),
	}

	periods := []config.Period{{Name: "第1-2节", Start: "08:00", End: "09:40"}}

	allSlots, err := svc.GetFreeUsersBySlot(context.Background(), 2, 1, 1, 0, periods)
	if err != nil {
		t.Fatalf("GetFreeUsersBySlot(all) error = %v", err)
	}
	if len(allSlots) != 1 {
		t.Fatalf("len(allSlots) = %d, want 1", len(allSlots))
	}
	gotAll := []string{allSlots[0].FreeUsers[0].Name, allSlots[0].FreeUsers[1].Name}
	slices.Sort(gotAll)
	if !slices.Equal(gotAll, []string{"张三", "李四"}) {
		t.Fatalf("all free users = %v, want [张三 李四]", gotAll)
	}

	filteredSlots, err := svc.GetFreeUsersBySlot(context.Background(), 2, 1, 1, deptA, periods)
	if err != nil {
		t.Fatalf("GetFreeUsersBySlot(filtered) error = %v", err)
	}
	if len(filteredSlots) != 1 {
		t.Fatalf("len(filteredSlots) = %d, want 1", len(filteredSlots))
	}
	if len(filteredSlots[0].FreeUsers) != 1 {
		t.Fatalf("len(filtered free users) = %d, want 1", len(filteredSlots[0].FreeUsers))
	}
	if filteredSlots[0].FreeUsers[0].Name != "张三" {
		t.Fatalf("filtered free user = %s, want 张三", filteredSlots[0].FreeUsers[0].Name)
	}
}
