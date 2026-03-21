package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"schedule_server/config"
	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/pkg/dingtalk"

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

func TestGetAttendanceDetailReturnsCurrentViewBeforeFinalize(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 10, 0, 0, time.Local),
		records: []dingtalk.CheckRecord{
			{
				DingUserID: fixtureDingUserIDOnTime,
				CheckTime:  time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
			{
				DingUserID: fixtureDingUserIDLate,
				CheckTime:  time.Date(2026, 3, 19, 8, 5, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
		},
	})

	resp, err := fixture.service.GetAttendanceDetail(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance detail: %v", err)
	}

	if resp.ViewMode != "current" {
		t.Fatalf("expected current view mode, got %q", resp.ViewMode)
	}
	if resp.IsFinalized {
		t.Fatalf("expected current view to be non-finalized")
	}

	wantFinalizeAt := time.Date(2026, 3, 19, 8, 30, 0, 0, time.Local)
	if !resp.FinalizeAt.Equal(wantFinalizeAt) {
		t.Fatalf("unexpected finalize_at: got %v want %v", resp.FinalizeAt, wantFinalizeAt)
	}

	if resp.Statistics.Late != 1 {
		t.Fatalf("expected 1 late user, got %d", resp.Statistics.Late)
	}
	if got := attendanceCheckNames(resp.Users.Late); !slices.Equal(got, []string{"LateUser"}) {
		t.Fatalf("unexpected late users: got %v", got)
	}

	if resp.Statistics.NotArrived != 1 {
		t.Fatalf("expected 1 current not arrived user, got %d", resp.Statistics.NotArrived)
	}
	if got := attendanceBasicNames(resp.Users.NotArrived); !slices.Equal(got, []string{"MissingUser"}) {
		t.Fatalf("unexpected current not arrived users: got %v", got)
	}
}

func TestGetAttendanceDetailReturnsFinalSnapshotAfterFinalize(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 40, 0, 0, time.Local),
		records: []dingtalk.CheckRecord{
			{
				DingUserID: fixtureDingUserIDOnTime,
				CheckTime:  time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
		},
	})

	dateOnly := time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local)
	onTimeJSON := mustMarshalJSON(t, []dto.StoredUserCheck{
		{
			ID:        fixture.users.onTime.ID,
			CheckTime: time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local).Unix(),
		},
	})
	lateJSON := mustMarshalJSON(t, []dto.StoredUserCheck{
		{
			ID:        fixture.users.late.ID,
			CheckTime: time.Date(2026, 3, 19, 8, 5, 0, 0, time.Local).Unix(),
		},
	})
	notArrivedJSON := mustMarshalJSON(t, []uint{fixture.users.missing.ID})

	record := &model.AttendanceRecord{
		Date:          dateOnly,
		Week:          fixture.request.Week,
		Section:       fixture.request.Section,
		OnTimeIDs:     onTimeJSON,
		LateIDs:       lateJSON,
		LeaveIDs:      "[]",
		NotArrivedIDs: notArrivedJSON,
		RestDayIDs:    "[]",
		HasCourseIDs:  "[]",
	}
	if err := fixture.recordRepo.Upsert(context.Background(), record); err != nil {
		t.Fatalf("save attendance record: %v", err)
	}

	resp, err := fixture.service.GetAttendanceDetail(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance detail: %v", err)
	}

	if resp.ViewMode != "final" {
		t.Fatalf("expected final view mode, got %q", resp.ViewMode)
	}
	if !resp.IsFinalized {
		t.Fatalf("expected final view to be finalized")
	}
	if resp.Statistics.Late != 1 {
		t.Fatalf("expected 1 late user from snapshot, got %d", resp.Statistics.Late)
	}
	if got := attendanceCheckNames(resp.Users.Late); !slices.Equal(got, []string{"LateUser"}) {
		t.Fatalf("unexpected late users from snapshot: got %v", got)
	}
}

func TestFinalizeAttendanceRecordPersistsLateAndNotArrived(t *testing.T) {
	var capturedEnd time.Time

	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 40, 0, 0, time.Local),
		records: []dingtalk.CheckRecord{
			{
				DingUserID: fixtureDingUserIDOnTime,
				CheckTime:  time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
			{
				DingUserID: fixtureDingUserIDLate,
				CheckTime:  time.Date(2026, 3, 19, 8, 5, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
		},
	})
	fixture.service.fetchAttendanceRecords = func(_ context.Context, _ []string, _ time.Time, endAt time.Time) ([]dingtalk.CheckRecord, error) {
		capturedEnd = endAt
		return fixture.records, nil
	}

	resp, err := fixture.service.FinalizeAttendanceRecord(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("finalize attendance record: %v", err)
	}

	recordDate := time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local)
	record, err := fixture.recordRepo.FindByDateSection(context.Background(), recordDate, fixture.request.Section)
	if err != nil {
		t.Fatalf("query attendance record: %v", err)
	}

	var lateUsers []dto.StoredUserCheck
	if err := json.Unmarshal([]byte(record.LateIDs), &lateUsers); err != nil {
		t.Fatalf("unmarshal late ids: %v", err)
	}
	if len(lateUsers) != 1 || lateUsers[0].ID != fixture.users.late.ID {
		t.Fatalf("unexpected late ids: %+v", lateUsers)
	}

	var notArrivedIDs []uint
	if err := json.Unmarshal([]byte(record.NotArrivedIDs), &notArrivedIDs); err != nil {
		t.Fatalf("unmarshal not_arrived ids: %v", err)
	}
	if !slices.Equal(notArrivedIDs, []uint{fixture.users.missing.ID}) {
		t.Fatalf("unexpected final not arrived ids: %v", notArrivedIDs)
	}

	wantFinalizeAt := time.Date(2026, 3, 19, 8, 30, 0, 0, time.Local)
	if !capturedEnd.Equal(wantFinalizeAt) {
		t.Fatalf("unexpected finalize query end: got %v want %v", capturedEnd, wantFinalizeAt)
	}
	if resp.ViewMode != "final" || !resp.IsFinalized {
		t.Fatalf("expected finalized response metadata, got view_mode=%q is_finalized=%v", resp.ViewMode, resp.IsFinalized)
	}
}

func TestFormatAttendanceTextFormatsCurrentBody(t *testing.T) {
	service := &AttendanceRecordService{}
	detail := &dto.AttendanceDetailResponse{
		Date:     "2026-03-13",
		Week:     2,
		Section:  1,
		ViewMode: "current",
		Statistics: dto.AttendanceStatistics{
			ShouldAttend: 8,
			OnTime:       2,
			Late:         1,
			Leave:        1,
			NotArrived:   1,
			RestDay:      1,
		},
		Users: dto.AttendanceUserLists{
			OnTime: []dto.AttendanceUserCheck{
				{Name: "曹浩博"},
				{Name: "熊恒智"},
			},
			Leave: []dto.AttendanceUserLeave{
				{Name: "韩思维"},
			},
			RestDay: []dto.AttendanceUserBasic{
				{Name: "小乐"},
			},
			Late: []dto.AttendanceUserCheck{
				{Name: "韩锐"},
			},
			NotArrived: []dto.AttendanceUserBasic{
				{Name: "小飞"},
			},
		},
	}

	resp := service.formatAttendanceText(detail, model.ScheduleModeSchool, []config.Period{{Name: "第1-2节"}})

	wantStatistics := "⬇️应到8人，准时打卡2人，请假1人，迟到1人，当前未到1人，休息1人"
	if resp.Statistics != wantStatistics {
		t.Fatalf("Statistics = %q, want %q", resp.Statistics, wantStatistics)
	}

	wantContent := []string{
		"🌟准时到(2人)：曹浩博、熊恒智",
		"⏳请假(1人)：韩思维",
		"😴休息日(1人)：小乐",
		"❗迟到(1人)：韩锐",
		"⏳当前未到(1人)：小飞",
	}
	if !slices.Equal(resp.Content, wantContent) {
		t.Fatalf("Content = %#v, want %#v", resp.Content, wantContent)
	}

	wantFullText := "📅 2026-03-13 周五 第2周 第1-2节 考勤\n" +
		wantStatistics + "\n" +
		"🌟准时到(2人)：曹浩博、熊恒智\n" +
		"⏳请假(1人)：韩思维\n" +
		"😴休息日(1人)：小乐\n" +
		"❗迟到(1人)：韩锐\n" +
		"⏳当前未到(1人)：小飞"
	if resp.FullText != wantFullText {
		t.Fatalf("FullText = %q, want %q", resp.FullText, wantFullText)
	}
}

func TestFormatAttendanceTextFormatsFinalBody(t *testing.T) {
	service := &AttendanceRecordService{}
	detail := &dto.AttendanceDetailResponse{
		Date:     "2026-03-13",
		Week:     2,
		Section:  1,
		ViewMode: "final",
		Statistics: dto.AttendanceStatistics{
			ShouldAttend: 6,
			OnTime:       2,
			Late:         1,
			Leave:        1,
			NotArrived:   2,
		},
		Users: dto.AttendanceUserLists{
			OnTime: []dto.AttendanceUserCheck{
				{Name: "曹浩博"},
				{Name: "熊恒智"},
			},
			Leave: []dto.AttendanceUserLeave{
				{Name: "韩思维"},
			},
			Late: []dto.AttendanceUserCheck{
				{Name: "韩锐"},
			},
			NotArrived: []dto.AttendanceUserBasic{
				{Name: "小飞"},
				{Name: "高婷"},
			},
		},
	}

	resp := service.formatAttendanceText(detail, model.ScheduleModeSchool, []config.Period{{Name: "第1-2节"}})

	wantStatistics := "⬇️应到6人，准时打卡2人，请假1人，迟到1人，未到2人"
	if resp.Statistics != wantStatistics {
		t.Fatalf("Statistics = %q, want %q", resp.Statistics, wantStatistics)
	}

	wantContent := []string{
		"🌟准时到(2人)：曹浩博、熊恒智",
		"⏳请假(1人)：韩思维",
		"❗迟到(1人)：韩锐",
		"⏳未到(2人)：小飞、高婷",
	}
	if !slices.Equal(resp.Content, wantContent) {
		t.Fatalf("Content = %#v, want %#v", resp.Content, wantContent)
	}

	wantFullText := "📅 2026-03-13 周五 第2周 第1-2节 考勤\n" +
		wantStatistics + "\n" +
		"🌟准时到(2人)：曹浩博、熊恒智\n" +
		"⏳请假(1人)：韩思维\n" +
		"❗迟到(1人)：韩锐\n" +
		"⏳未到(2人)：小飞、高婷"
	if resp.FullText != wantFullText {
		t.Fatalf("FullText = %q, want %q", resp.FullText, wantFullText)
	}
}

const (
	fixtureTenantID          = uint(1)
	fixtureDeptID            = int64(101)
	fixtureDingUserIDOnTime  = "user-on-time"
	fixtureDingUserIDLate    = "user-late"
	fixtureDingUserIDMissing = "user-missing"
)

type attendanceRealtimeFixtureOptions struct {
	now     time.Time
	records []dingtalk.CheckRecord
}

type attendanceRealtimeFixture struct {
	db         *gorm.DB
	service    *AttendanceRecordService
	recordRepo repository.AttendanceRecordRepository
	request    *dto.AttendanceDetailRequest
	users      attendanceRealtimeUsers
	records    []dingtalk.CheckRecord
}

type attendanceRealtimeUsers struct {
	onTime  model.User
	late    model.User
	missing model.User
}

func newAttendanceRealtimeFixture(t *testing.T, opts attendanceRealtimeFixtureOptions) attendanceRealtimeFixture {
	t.Helper()

	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(
		&model.User{},
		&model.Department{},
		&model.UserDepartment{},
		&model.Course{},
		&model.LeaveApproval{},
		&model.AttendanceRecord{},
		&model.UserRestDay{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	dept := model.Department{TenantID: fixtureTenantID, DeptID: fixtureDeptID, Name: "行政部", IsLeaf: true, Status: 1}
	if err := db.Create(&dept).Error; err != nil {
		t.Fatalf("create department: %v", err)
	}

	users := []model.User{
		{TenantID: fixtureTenantID, DingUserID: fixtureDingUserIDOnTime, Name: "OnTimeUser", Status: 1},
		{TenantID: fixtureTenantID, DingUserID: fixtureDingUserIDLate, Name: "LateUser", Status: 1},
		{TenantID: fixtureTenantID, DingUserID: fixtureDingUserIDMissing, Name: "MissingUser", Status: 1},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}

	userDepts := []model.UserDepartment{
		{TenantID: fixtureTenantID, UserID: users[0].ID, DeptID: fixtureDeptID},
		{TenantID: fixtureTenantID, UserID: users[1].ID, DeptID: fixtureDeptID},
		{TenantID: fixtureTenantID, UserID: users[2].ID, DeptID: fixtureDeptID},
	}
	if err := db.Create(&userDepts).Error; err != nil {
		t.Fatalf("create user departments: %v", err)
	}

	recordRepo := repository.NewAttendanceRecordRepository(db)
	service := &AttendanceRecordService{
		userRepo:             repository.NewUserRepository(db),
		courseRepo:           repository.NewCourseRepository(db),
		leaveRepo:            repository.NewLeaveApprovalRepository(db),
		attendanceRecordRepo: recordRepo,
		restDayRepo:          repository.NewUserRestDayRepository(db),
		scheduleCfg:          config.Schedule{LateGraceMinutes: 1, Periods: []config.Period{{Name: "第一节", Start: "08:00", End: "09:40"}}},
		logger:               zap.NewNop().Sugar(),
		nowFn:                func() time.Time { return opts.now },
		fetchAttendanceRecords: func(_ context.Context, _ []string, _, _ time.Time) ([]dingtalk.CheckRecord, error) {
			return opts.records, nil
		},
	}

	return attendanceRealtimeFixture{
		db:         db,
		service:    service,
		recordRepo: recordRepo,
		request: &dto.AttendanceDetailRequest{
			Date:    "2026-03-19",
			Week:    3,
			Section: 1,
		},
		users: attendanceRealtimeUsers{
			onTime:  users[0],
			late:    users[1],
			missing: users[2],
		},
		records: opts.records,
	}
}

func attendanceCheckNames(users []dto.AttendanceUserCheck) []string {
	names := make([]string, 0, len(users))
	for _, user := range users {
		names = append(names, user.Name)
	}
	slices.Sort(names)
	return names
}

func attendanceBasicNames(users []dto.AttendanceUserBasic) []string {
	names := make([]string, 0, len(users))
	for _, user := range users {
		names = append(names, user.Name)
	}
	slices.Sort(names)
	return names
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(data)
}
