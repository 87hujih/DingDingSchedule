package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"schedule_server/config"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetShouldAttendUsersExcludesUsersFromDisabledDepartments(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:attendance-record-service-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(&model.User{}, &model.Department{}, &model.UserDepartment{}, &model.Course{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	const tenantID = uint(1)

	activeDept := model.Department{TenantID: tenantID, DeptID: 101, Name: "active", IsLeaf: true, Status: 1}
	inactiveDept := model.Department{TenantID: tenantID, DeptID: 102, Name: "inactive", IsLeaf: true, Status: 1}
	if err := db.Create(&[]model.Department{activeDept, inactiveDept}).Error; err != nil {
		t.Fatalf("create departments: %v", err)
	}
	if err := db.Model(&model.Department{}).Where("tenant_id = ? AND dept_id = ?", tenantID, inactiveDept.DeptID).Update("status", 0).Error; err != nil {
		t.Fatalf("disable department: %v", err)
	}

	users := []model.User{
		{TenantID: tenantID, DingUserID: "enabled-user", Name: "EnabledUser", Status: 1},
		{TenantID: tenantID, DingUserID: "disabled-dept-user", Name: "DisabledDeptUser", Status: 1},
		{TenantID: tenantID, DingUserID: "disabled-user", Name: "DisabledUser", Status: 1},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	if err := db.Model(&model.User{}).Where("id = ?", users[2].ID).Update("status", 0).Error; err != nil {
		t.Fatalf("disable user: %v", err)
	}

	userDepts := []model.UserDepartment{
		{TenantID: tenantID, UserID: users[0].ID, DeptID: activeDept.DeptID},
		{TenantID: tenantID, UserID: users[1].ID, DeptID: inactiveDept.DeptID},
		{TenantID: tenantID, UserID: users[2].ID, DeptID: activeDept.DeptID},
	}
	if err := db.Create(&userDepts).Error; err != nil {
		t.Fatalf("create user departments: %v", err)
	}

	svc := &AttendanceRecordService{
		userRepo:    repository.NewUserRepository(db),
		courseRepo:  repository.NewCourseRepository(db),
		scheduleCfg: config.Schedule{},
		logger:      zap.NewNop().Sugar(),
		semesterSrv: nil,
		dingMgr:     nil,
		restDayRepo: nil,
		leaveRepo:   nil,
	}

	shouldAttend, hasCourse, err := svc.getShouldAttendUsers(context.Background(), time.Date(2026, 3, 18, 0, 0, 0, 0, time.Local), 3, 1, nil)
	if err != nil {
		t.Fatalf("get should attend users: %v", err)
	}

	if len(hasCourse) != 0 {
		t.Fatalf("expected no course users, got %d", len(hasCourse))
	}

	gotNames := make([]string, 0, len(shouldAttend))
	for _, user := range shouldAttend {
		gotNames = append(gotNames, user.Name)
	}
	slices.Sort(gotNames)

	wantNames := []string{"EnabledUser"}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("unexpected should attend users: got %v want %v", gotNames, wantNames)
	}
}
