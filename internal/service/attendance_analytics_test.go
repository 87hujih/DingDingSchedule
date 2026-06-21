package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"schedule_server/internal/agent/tools"
	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetWeeklyRankingCountsLateIDsOnly(t *testing.T) {
	db := newAttendanceAnalyticsTestDB(t)

	dept := model.Department{TenantID: 1, DeptID: 101, Name: "行政部", IsLeaf: true, Status: 1}
	if err := db.Create(&dept).Error; err != nil {
		t.Fatalf("create department: %v", err)
	}

	users := []model.User{
		{TenantID: 1, DingUserID: "late-user", Name: "LateUser", Status: 1},
		{TenantID: 1, DingUserID: "missing-user", Name: "MissingUser", Status: 1},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	if err := db.Create(&[]model.UserDepartment{
		{TenantID: 1, UserID: users[0].ID, DeptID: dept.DeptID},
		{TenantID: 1, UserID: users[1].ID, DeptID: dept.DeptID},
	}).Error; err != nil {
		t.Fatalf("create user departments: %v", err)
	}

	recordRepo := repository.NewAttendanceRecordRepository(db)
	recordDate := beginningOfCurrentWeek(time.Now()).AddDate(0, 0, 1)
	lateJSON := mustAttendanceAnalyticsJSON(t, []dto.StoredUserCheck{
		{ID: users[0].ID, CheckTime: recordDate.Add(8 * time.Hour).Unix()},
	})
	missingJSON := mustAttendanceAnalyticsJSON(t, []uint{users[1].ID})

	for section := 1; section <= 2; section++ {
		if err := recordRepo.Upsert(context.Background(), &model.AttendanceRecord{
			Date:          recordDate,
			Week:          3,
			Section:       section,
			LateIDs:       lateJSON,
			NotArrivedIDs: missingJSON,
			OnTimeIDs:     "[]",
			LeaveIDs:      "[]",
			RestDayIDs:    "[]",
			HasCourseIDs:  "[]",
		}); err != nil {
			t.Fatalf("save attendance record: %v", err)
		}
	}

	service := &AttendanceRecordService{
		userRepo:             repository.NewUserRepository(db),
		attendanceRecordRepo: recordRepo,
		logger:               zap.NewNop().Sugar(),
	}

	resp, err := service.GetWeeklyRanking(context.Background(), &dto.WeeklyAttendanceRankingRequest{})
	if err != nil {
		t.Fatalf("GetWeeklyRanking() error = %v", err)
	}

	if len(resp.Items) != 1 {
		t.Fatalf("ranking item count = %d, want 1", len(resp.Items))
	}
	if resp.Items[0].Name != "LateUser" {
		t.Fatalf("ranking name = %q, want LateUser", resp.Items[0].Name)
	}
	if resp.Items[0].LateCount != 2 {
		t.Fatalf("ranking late count = %d, want 2", resp.Items[0].LateCount)
	}
}

func TestQueryStatsTreatsFinalNotArrivedAsAbsentOnly(t *testing.T) {
	db := newAttendanceAnalyticsTestDB(t)

	dept := model.Department{TenantID: 1, DeptID: 101, Name: "行政部", IsLeaf: true, Status: 1}
	if err := db.Create(&dept).Error; err != nil {
		t.Fatalf("create department: %v", err)
	}

	users := []model.User{
		{TenantID: 1, DingUserID: "on-time", Name: "OnTimeUser", Status: 1},
		{TenantID: 1, DingUserID: "late", Name: "LateUser", Status: 1},
		{TenantID: 1, DingUserID: "absent", Name: "AbsentUser", Status: 1},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	if err := db.Create(&[]model.UserDepartment{
		{TenantID: 1, UserID: users[0].ID, DeptID: dept.DeptID},
		{TenantID: 1, UserID: users[1].ID, DeptID: dept.DeptID},
		{TenantID: 1, UserID: users[2].ID, DeptID: dept.DeptID},
	}).Error; err != nil {
		t.Fatalf("create user departments: %v", err)
	}

	recordRepo := repository.NewAttendanceRecordRepository(db)
	recordDate := time.Now().Format("2006-01-02")
	onTimeJSON := mustAttendanceAnalyticsJSON(t, []dto.StoredUserCheck{
		{ID: users[0].ID, CheckTime: time.Now().Unix()},
	})
	lateJSON := mustAttendanceAnalyticsJSON(t, []dto.StoredUserCheck{
		{ID: users[1].ID, CheckTime: time.Now().Add(5 * time.Minute).Unix()},
	})
	notArrivedJSON := mustAttendanceAnalyticsJSON(t, []uint{users[2].ID})

	if err := recordRepo.Upsert(context.Background(), &model.AttendanceRecord{
		Date:          time.Now(),
		Week:          3,
		Section:       1,
		OnTimeIDs:     onTimeJSON,
		LateIDs:       lateJSON,
		NotArrivedIDs: notArrivedJSON,
		LeaveIDs:      "[]",
		RestDayIDs:    "[]",
		HasCourseIDs:  "[]",
	}); err != nil {
		t.Fatalf("save attendance record: %v", err)
	}

	service := &AttendanceRecordService{
		userRepo:             repository.NewUserRepository(db),
		attendanceRecordRepo: recordRepo,
		logger:               zap.NewNop().Sugar(),
	}

	items, err := service.QueryStats(context.Background(), tools.AttendanceStatsQuery{
		Date:    recordDate,
		GroupBy: "day",
	})
	if err != nil {
		t.Fatalf("QueryStats() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("stats item count = %d, want 1", len(items))
	}
	if items[0].AbsentCount != 1 {
		t.Fatalf("AbsentCount = %d, want 1", items[0].AbsentCount)
	}
}

func newAttendanceAnalyticsTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := "file:attendance-analytics-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Department{}, &model.UserDepartment{}, &model.AttendanceRecord{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func mustAttendanceAnalyticsJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(data)
}

func beginningOfCurrentWeek(now time.Time) time.Time {
	offset := int(time.Monday - now.Weekday())
	if offset > 0 {
		offset = -6
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, offset)
}
