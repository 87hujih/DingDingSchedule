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
	"schedule_server/internal/response"
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

func TestGetAttendanceDetailSnapshotUsesAttendanceCandidateDeptFilter(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 40, 0, 0, time.Local),
	})

	disabledDept := model.Department{
		TenantID: fixtureTenantID,
		DeptID:   202,
		Name:     "停用部门",
		IsLeaf:   true,
		Status:   1,
	}
	if err := fixture.db.Create(&disabledDept).Error; err != nil {
		t.Fatalf("create disabled department: %v", err)
	}
	if err := fixture.db.Model(&model.Department{}).
		Where("tenant_id = ? AND dept_id = ?", fixtureTenantID, disabledDept.DeptID).
		Update("status", 0).Error; err != nil {
		t.Fatalf("disable department: %v", err)
	}

	disabledUser := model.User{
		TenantID:   fixtureTenantID,
		DingUserID: "disabled-dept-user",
		Name:       "DisabledDeptUser",
		Status:     1,
	}
	if err := fixture.db.Create(&disabledUser).Error; err != nil {
		t.Fatalf("create disabled department user: %v", err)
	}
	if err := fixture.db.Create(&model.UserDepartment{
		TenantID: fixtureTenantID,
		UserID:   disabledUser.ID,
		DeptID:   disabledDept.DeptID,
	}).Error; err != nil {
		t.Fatalf("create disabled user department: %v", err)
	}

	dateOnly := time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local)
	record := &model.AttendanceRecord{
		Date:          dateOnly,
		Week:          fixture.request.Week,
		Section:       fixture.request.Section,
		OnTimeIDs:     mustMarshalJSON(t, []dto.StoredUserCheck{{ID: disabledUser.ID, CheckTime: time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local).Unix()}}),
		LateIDs:       "[]",
		LeaveIDs:      "[]",
		NotArrivedIDs: "[]",
		RestDayIDs:    "[]",
		HasCourseIDs:  "[]",
	}
	if err := fixture.recordRepo.Upsert(context.Background(), record); err != nil {
		t.Fatalf("save attendance record: %v", err)
	}

	req := &dto.AttendanceDetailRequest{
		Date:    fixture.request.Date,
		Week:    fixture.request.Week,
		Section: fixture.request.Section,
		DeptIDs: []int64{disabledDept.DeptID},
	}

	candidates, err := fixture.service.userRepo.ListAttendanceCandidates(context.Background(), req.DeptIDs)
	if err != nil {
		t.Fatalf("list attendance candidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected disabled department to have no attendance candidates, got %+v", candidates)
	}

	resp, err := fixture.service.GetAttendanceDetail(context.Background(), req)
	if err != nil {
		t.Fatalf("get attendance detail: %v", err)
	}

	if resp.Statistics.ShouldAttend != 0 {
		t.Fatalf("expected disabled department snapshot filter to exclude non-candidates, got should_attend=%d", resp.Statistics.ShouldAttend)
	}
	if len(resp.Users.OnTime) != 0 {
		t.Fatalf("expected disabled department snapshot filter to exclude on-time users, got %+v", resp.Users.OnTime)
	}
}

func TestAttendanceDetailPrioritizesRestDayAndLeaveOverHasCourse(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 10, 0, 0, time.Local),
	})

	priorityUsers := []model.User{
		{TenantID: fixtureTenantID, DingUserID: "user-has-course", Name: "HasCourseUser", Status: 1},
		{TenantID: fixtureTenantID, DingUserID: "user-leave-course", Name: "LeaveCourseUser", Status: 1},
		{TenantID: fixtureTenantID, DingUserID: "user-rest-course", Name: "RestDayCourseUser", Status: 1},
	}
	if err := fixture.db.Create(&priorityUsers).Error; err != nil {
		t.Fatalf("create priority users: %v", err)
	}

	priorityUserDepts := []model.UserDepartment{
		{TenantID: fixtureTenantID, UserID: priorityUsers[0].ID, DeptID: fixtureDeptID},
		{TenantID: fixtureTenantID, UserID: priorityUsers[1].ID, DeptID: fixtureDeptID},
		{TenantID: fixtureTenantID, UserID: priorityUsers[2].ID, DeptID: fixtureDeptID},
	}
	if err := fixture.db.Create(&priorityUserDepts).Error; err != nil {
		t.Fatalf("create priority user departments: %v", err)
	}

	courses := []model.Course{
		{TenantID: fixtureTenantID, UserID: priorityUsers[0].ID, CourseName: "课程A", DayOfWeek: 4, Section: fixture.request.Section, WeekList: "3"},
		{TenantID: fixtureTenantID, UserID: priorityUsers[1].ID, CourseName: "课程B", DayOfWeek: 4, Section: fixture.request.Section, WeekList: "3"},
		{TenantID: fixtureTenantID, UserID: priorityUsers[2].ID, CourseName: "课程C", DayOfWeek: 4, Section: fixture.request.Section, WeekList: "3"},
	}
	if err := fixture.db.Create(&courses).Error; err != nil {
		t.Fatalf("create courses: %v", err)
	}

	dayOfWeek := 4
	if err := fixture.db.Create(&model.UserRestDay{TenantID: fixtureTenantID, UserID: priorityUsers[2].ID, DayOfWeek: &dayOfWeek}).Error; err != nil {
		t.Fatalf("create rest day: %v", err)
	}

	leaveStart := time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local)
	leaveEnd := time.Date(2026, 3, 19, 9, 40, 0, 0, time.Local)
	leave := &model.LeaveApproval{
		TenantID:          fixtureTenantID,
		ProcessInstanceID: "pi-leave-course-user",
		DingUserID:        priorityUsers[1].DingUserID,
		UserID:            priorityUsers[1].ID,
		UserName:          priorityUsers[1].Name,
		StartAt:           leaveStart,
		EndAt:             leaveEnd,
		LeaveType:         "事假",
		Reason:            "优先级测试",
	}
	if err := fixture.db.Create(leave).Error; err != nil {
		t.Fatalf("create leave approval: %v", err)
	}

	assertPriority := func(label string, resp *dto.AttendanceDetailResponse) {
		t.Helper()
		if resp.Statistics.RestDay != 1 {
			t.Fatalf("%s: expected 1 rest day user, got %d", label, resp.Statistics.RestDay)
		}
		if resp.Statistics.Leave != 1 {
			t.Fatalf("%s: expected 1 leave user, got %d", label, resp.Statistics.Leave)
		}
		if resp.Statistics.HasCourse != 1 {
			t.Fatalf("%s: expected 1 has-course user, got %d", label, resp.Statistics.HasCourse)
		}
		if resp.Statistics.ShouldAttend != 4 {
			t.Fatalf("%s: expected should_attend=4 after excluding rest day and has-course, got %d", label, resp.Statistics.ShouldAttend)
		}
		if got := attendanceBasicNames(resp.Users.RestDay); !slices.Equal(got, []string{"RestDayCourseUser"}) {
			t.Fatalf("%s: unexpected rest day users: got %v", label, got)
		}
		leaveNames := make([]string, 0, len(resp.Users.Leave))
		for _, user := range resp.Users.Leave {
			leaveNames = append(leaveNames, user.Name)
		}
		slices.Sort(leaveNames)
		if !slices.Equal(leaveNames, []string{"LeaveCourseUser"}) {
			t.Fatalf("%s: unexpected leave users: got %v", label, leaveNames)
		}
		if got := attendanceBasicNames(resp.Users.HasCourse); !slices.Equal(got, []string{"HasCourseUser"}) {
			t.Fatalf("%s: unexpected has-course users: got %v", label, got)
		}
		if got := attendanceBasicNames(resp.Users.NotArrived); !slices.Equal(got, []string{"LateUser", "MissingUser", "OnTimeUser"}) {
			t.Fatalf("%s: unexpected not-arrived users: got %v", label, got)
		}
	}

	current, err := fixture.service.GetAttendanceDetail(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance detail: %v", err)
	}
	assertPriority("current detail", current)

	finalized, err := fixture.service.FinalizeAttendanceRecord(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("finalize attendance record: %v", err)
	}
	assertPriority("finalize response", finalized)

	snapshot, err := fixture.service.GetAttendanceRecordFromDB(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance record from db: %v", err)
	}
	assertPriority("snapshot detail", snapshot)
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

func TestSignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride(t *testing.T) {
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

	resp, err := fixture.service.SignForUsers(context.Background(), &dto.SignForUserRequest{
		Date:          fixture.request.Date,
		Section:       fixture.request.Section,
		TargetUserIDs: []uint{fixture.users.late.ID},
	})
	if err != nil {
		t.Fatalf("sign for users: %v", err)
	}
	if !slices.Equal(resp.SuccessIDs, []uint{fixture.users.late.ID}) {
		t.Fatalf("unexpected success ids: %+v", resp.SuccessIDs)
	}

	detail, err := fixture.service.GetAttendanceDetail(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance detail: %v", err)
	}

	if got := attendanceCheckNames(detail.Users.OnTime); !slices.Equal(got, []string{"LateUser", "OnTimeUser"}) {
		t.Fatalf("unexpected on_time users after realtime override: %v", got)
	}
	if got := attendanceCheckNames(detail.Users.Late); len(got) != 0 {
		t.Fatalf("expected late users to be empty after realtime override, got %v", got)
	}
	if got := attendanceBasicNames(detail.Users.NotArrived); !slices.Equal(got, []string{"MissingUser"}) {
		t.Fatalf("unexpected not_arrived users after realtime override: %v", got)
	}
}

func TestSignForUsersRejectsRealtimeOverridesForNonAttendTargets(t *testing.T) {
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

	_, err := fixture.service.SignForUsers(context.Background(), &dto.SignForUserRequest{
		Date:          fixture.request.Date,
		Section:       fixture.request.Section,
		TargetUserIDs: []uint{fixture.users.onTime.ID},
	})
	if err == nil {
		t.Fatalf("expected realtime override for non-attend target to fail")
	}
	if !response.IsBizError(err) {
		t.Fatalf("expected business error, got %T: %v", err, err)
	}

	overrides, err := fixture.manualOverrideRepo.ListByDateSection(context.Background(), time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local), fixture.request.Section)
	if err != nil {
		t.Fatalf("list manual overrides: %v", err)
	}
	if len(overrides) != 0 {
		t.Fatalf("expected no manual override records, got %+v", overrides)
	}
}

func TestFinalizeAttendanceRecordKeepsManualOverrideOverLatePunch(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 40, 0, 0, time.Local),
		records: []dingtalk.CheckRecord{
			{
				DingUserID: fixtureDingUserIDLate,
				CheckTime:  time.Date(2026, 3, 19, 8, 5, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
		},
	})

	manualOverride := &model.AttendanceManualOverride{
		TenantID:     fixtureTenantID,
		Date:         time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local),
		Week:         fixture.request.Week,
		Section:      fixture.request.Section,
		UserID:       fixture.users.late.ID,
		OverrideType: "force_on_time",
		OperatorID:   fixture.users.onTime.ID,
		AppliedAt:    time.Date(2026, 3, 19, 8, 12, 0, 0, time.Local),
	}
	if err := fixture.manualOverrideRepo.UpsertForceOnTime(context.Background(), manualOverride); err != nil {
		t.Fatalf("create manual override: %v", err)
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

	storedOnTime := mustStoredUserChecks(t, record.OnTimeIDs)
	if len(storedOnTime) != 1 || storedOnTime[0].ID != fixture.users.late.ID {
		t.Fatalf("expected manual override to keep late punch in on_time, got %+v", storedOnTime)
	}
	storedLate := mustStoredUserChecks(t, record.LateIDs)
	if len(storedLate) != 0 {
		t.Fatalf("expected late list to be empty after override, got %+v", storedLate)
	}
	if resp.ViewMode != "final" || !resp.IsFinalized {
		t.Fatalf("expected finalized response metadata, got view_mode=%q is_finalized=%v", resp.ViewMode, resp.IsFinalized)
	}
}

func TestSignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent(t *testing.T) {
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

	recordDate := time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local)
	record := &model.AttendanceRecord{
		Date:          recordDate,
		Week:          fixture.request.Week,
		Section:       fixture.request.Section,
		OnTimeIDs:     mustMarshalJSON(t, []dto.StoredUserCheck{{ID: fixture.users.onTime.ID, CheckTime: time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local).Unix()}}),
		LateIDs:       mustMarshalJSON(t, []dto.StoredUserCheck{{ID: fixture.users.late.ID, CheckTime: time.Date(2026, 3, 19, 8, 5, 0, 0, time.Local).Unix()}}),
		LeaveIDs:      "[]",
		NotArrivedIDs: mustMarshalJSON(t, []uint{fixture.users.missing.ID}),
		RestDayIDs:    "[]",
		HasCourseIDs:  "[]",
	}
	if err := fixture.recordRepo.Upsert(context.Background(), record); err != nil {
		t.Fatalf("save attendance record: %v", err)
	}

	_, err := fixture.service.SignForUsers(context.Background(), &dto.SignForUserRequest{
		RecordID:      record.ID,
		TargetUserIDs: []uint{fixture.users.late.ID},
	})
	if err != nil {
		t.Fatalf("sign for users with record_id: %v", err)
	}

	snapshot, err := fixture.service.GetAttendanceRecordFromDB(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance record from db: %v", err)
	}
	detail, err := fixture.service.GetAttendanceDetail(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance detail: %v", err)
	}

	if got := attendanceCheckNames(snapshot.Users.OnTime); !slices.Equal(got, attendanceCheckNames(detail.Users.OnTime)) {
		t.Fatalf("snapshot and detail on_time diverged: snapshot=%v detail=%v", got, attendanceCheckNames(detail.Users.OnTime))
	}
	if got := attendanceCheckNames(snapshot.Users.Late); !slices.Equal(got, attendanceCheckNames(detail.Users.Late)) {
		t.Fatalf("snapshot and detail late diverged: snapshot=%v detail=%v", got, attendanceCheckNames(detail.Users.Late))
	}
	if got := attendanceBasicNames(snapshot.Users.NotArrived); !slices.Equal(got, attendanceBasicNames(detail.Users.NotArrived)) {
		t.Fatalf("snapshot and detail not_arrived diverged: snapshot=%v detail=%v", got, attendanceBasicNames(detail.Users.NotArrived))
	}

	overrides, err := fixture.manualOverrideRepo.ListByDateSection(context.Background(), time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local), fixture.request.Section)
	if err != nil {
		t.Fatalf("list manual overrides: %v", err)
	}
	if len(overrides) == 0 {
		t.Fatalf("expected manual override record to exist after historical sign")
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
	db                 *gorm.DB
	service            *AttendanceRecordService
	recordRepo         repository.AttendanceRecordRepository
	manualOverrideRepo repository.AttendanceManualOverrideRepository
	request            *dto.AttendanceDetailRequest
	users              attendanceRealtimeUsers
	records            []dingtalk.CheckRecord
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
		&model.AttendanceManualOverride{},
		&model.UserRestDay{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	dept := model.Department{TenantID: fixtureTenantID, DeptID: fixtureDeptID, Name: "行政部", IsLeaf: true, Status: 1}
	if err := db.Create(&dept).Error; err != nil {
		t.Fatalf("create department: %v", err)
	}

	users := []model.User{
		{TenantID: fixtureTenantID, DingUserID: fixtureDingUserIDOnTime, Name: "OnTimeUser", Avatar: "on-time.png", Status: 1},
		{TenantID: fixtureTenantID, DingUserID: fixtureDingUserIDLate, Name: "LateUser", Avatar: "late.png", Status: 1},
		{TenantID: fixtureTenantID, DingUserID: fixtureDingUserIDMissing, Name: "MissingUser", Avatar: "missing.png", Status: 1},
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
		manualOverrideRepo:   repository.NewAttendanceManualOverrideRepository(db),
		restDayRepo:          repository.NewUserRestDayRepository(db),
		scheduleCfg:          config.Schedule{LateGraceMinutes: 1, Periods: []config.Period{{Name: "第一节", Start: "08:00", End: "09:40"}}},
		logger:               zap.NewNop().Sugar(),
		nowFn:                func() time.Time { return opts.now },
		fetchAttendanceRecords: func(_ context.Context, _ []string, _, _ time.Time) ([]dingtalk.CheckRecord, error) {
			return opts.records, nil
		},
	}

	return attendanceRealtimeFixture{
		db:                 db,
		service:            service,
		recordRepo:         recordRepo,
		manualOverrideRepo: repository.NewAttendanceManualOverrideRepository(db),
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

func TestGetAttendanceDetailRealtimePopulatesAvatarAndDeptName(t *testing.T) {
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

	assertBasic := func(label string, users []dto.AttendanceUserBasic, wantName, wantAvatar, wantDept string) {
		t.Helper()
		for _, user := range users {
			if user.Name != wantName {
				continue
			}
			if user.Avatar != wantAvatar || user.DeptName != wantDept {
				t.Fatalf("%s user profile mismatch: got avatar=%q dept=%q want avatar=%q dept=%q", label, user.Avatar, user.DeptName, wantAvatar, wantDept)
			}
			return
		}
		t.Fatalf("%s user %q not found", label, wantName)
	}

	assertCheck := func(label string, users []dto.AttendanceUserCheck, wantName, wantAvatar, wantDept string) {
		t.Helper()
		for _, user := range users {
			if user.Name != wantName {
				continue
			}
			if user.Avatar != wantAvatar || user.DeptName != wantDept {
				t.Fatalf("%s user profile mismatch: got avatar=%q dept=%q want avatar=%q dept=%q", label, user.Avatar, user.DeptName, wantAvatar, wantDept)
			}
			return
		}
		t.Fatalf("%s user %q not found", label, wantName)
	}

	assertBasic("should_attend", resp.Users.ShouldAttend, "MissingUser", "missing.png", "行政部")
	assertCheck("on_time", resp.Users.OnTime, "OnTimeUser", "on-time.png", "行政部")
	assertCheck("late", resp.Users.Late, "LateUser", "late.png", "行政部")
	assertBasic("not_arrived", resp.Users.NotArrived, "MissingUser", "missing.png", "行政部")
}

func TestGetAttendanceDetailDeduplicatesMultiplePunchesFromSameUser(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 10, 0, 0, time.Local),
		records: []dingtalk.CheckRecord{
			{
				DingUserID: fixtureDingUserIDOnTime,
				CheckTime:  time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
			{
				DingUserID: fixtureDingUserIDOnTime,
				CheckTime:  time.Date(2026, 3, 19, 8, 2, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
		},
	})

	resp, err := fixture.service.GetAttendanceDetail(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance detail: %v", err)
	}

	if resp.Statistics.OnTime != 1 {
		t.Fatalf("expected 1 on-time user after duplicate punches, got %d", resp.Statistics.OnTime)
	}
	if len(resp.Users.OnTime) != 1 {
		t.Fatalf("expected on_time list to contain 1 user, got %+v", resp.Users.OnTime)
	}
	if got := attendanceCheckNames(resp.Users.OnTime); !slices.Equal(got, []string{"OnTimeUser"}) {
		t.Fatalf("unexpected on_time users: got %v", got)
	}

	wantCheckTime := time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local)
	if !resp.Users.OnTime[0].CheckTime.Equal(wantCheckTime) {
		t.Fatalf("expected earliest check time %v, got %v", wantCheckTime, resp.Users.OnTime[0].CheckTime)
	}
}

func TestGetAttendanceDetailCarryForwardIncludesPreviousLateUsers(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 55, 0, 0, time.Local),
		records: []dingtalk.CheckRecord{
			{
				DingUserID: fixtureDingUserIDOnTime,
				CheckTime:  time.Date(2026, 3, 19, 8, 50, 0, 0, time.Local),
				CheckType:  "OnDuty",
			},
		},
	})

	fixture.service.scheduleCfg.Periods = []config.Period{
		{Name: "第一节", Start: "08:00", End: "08:45"},
		{Name: "第二节", Start: "08:50", End: "09:35"},
	}
	fixture.service.scheduleCfg.MaxCarryForwardGapMinutes = 10
	fixture.request.Section = 2

	if err := fixture.db.Create(&model.Course{
		TenantID:   fixtureTenantID,
		UserID:     fixture.users.missing.ID,
		CourseName: "高数",
		DayOfWeek:  4,
		Section:    2,
		WeekList:   "3",
	}).Error; err != nil {
		t.Fatalf("create section-2 course: %v", err)
	}

	dateOnly := time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local)
	prevRecord := &model.AttendanceRecord{
		Date:    dateOnly,
		Week:    fixture.request.Week,
		Section: 1,
		OnTimeIDs: mustMarshalJSON(t, []dto.StoredUserCheck{
			{
				ID:        fixture.users.onTime.ID,
				CheckTime: time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local).Unix(),
			},
		}),
		LateIDs: mustMarshalJSON(t, []dto.StoredUserCheck{
			{
				ID:        fixture.users.late.ID,
				CheckTime: time.Date(2026, 3, 19, 8, 5, 0, 0, time.Local).Unix(),
			},
			{
				ID:        fixture.users.missing.ID,
				CheckTime: time.Date(2026, 3, 19, 8, 6, 0, 0, time.Local).Unix(),
			},
		}),
		LeaveIDs:      "[]",
		NotArrivedIDs: "[]",
		RestDayIDs:    "[]",
		HasCourseIDs:  "[]",
	}
	if err := fixture.recordRepo.Upsert(context.Background(), prevRecord); err != nil {
		t.Fatalf("save previous section record: %v", err)
	}

	resp, err := fixture.service.GetAttendanceDetail(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("get attendance detail: %v", err)
	}

	if resp.Statistics.OnTime != 2 {
		t.Fatalf("expected 2 on_time users after carry-forward, got %d", resp.Statistics.OnTime)
	}
	if got := attendanceCheckNames(resp.Users.OnTime); !slices.Equal(got, []string{"LateUser", "OnTimeUser"}) {
		t.Fatalf("unexpected on_time users after carry-forward: got %v", got)
	}
	if resp.Statistics.NotArrived != 0 {
		t.Fatalf("expected 0 not_arrived users after carry-forward, got %d", resp.Statistics.NotArrived)
	}
	if got := attendanceBasicNames(resp.Users.HasCourse); !slices.Equal(got, []string{"MissingUser"}) {
		t.Fatalf("unexpected has_course users: got %v", got)
	}
}

func mustStoredUserChecks(t *testing.T, raw string) []dto.StoredUserCheck {
	t.Helper()

	var users []dto.StoredUserCheck
	if err := json.Unmarshal([]byte(raw), &users); err != nil {
		t.Fatalf("unmarshal attendance users: %v", err)
	}
	return users
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
