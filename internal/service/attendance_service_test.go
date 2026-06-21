package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"schedule_server/config"
	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"

	"go.uber.org/zap"
)

func TestSlotAttendanceStatusPrioritizesRestDayAndLeaveOverHasCourse(t *testing.T) {
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
		{TenantID: fixtureTenantID, UserID: priorityUsers[0].ID, CourseName: "CourseA", DayOfWeek: 4, Section: fixture.request.Section, WeekList: "3"},
		{TenantID: fixtureTenantID, UserID: priorityUsers[1].ID, CourseName: "CourseB", DayOfWeek: 4, Section: fixture.request.Section, WeekList: "3"},
		{TenantID: fixtureTenantID, UserID: priorityUsers[2].ID, CourseName: "CourseC", DayOfWeek: 4, Section: fixture.request.Section, WeekList: "3"},
	}
	if err := fixture.db.Create(&courses).Error; err != nil {
		t.Fatalf("create courses: %v", err)
	}

	dayOfWeek := 4
	if err := fixture.db.Create(&model.UserRestDay{TenantID: fixtureTenantID, UserID: priorityUsers[2].ID, DayOfWeek: &dayOfWeek}).Error; err != nil {
		t.Fatalf("create rest day: %v", err)
	}

	leave := &model.LeaveApproval{
		TenantID:          fixtureTenantID,
		ProcessInstanceID: "pi-leave-course-user",
		DingUserID:        priorityUsers[1].DingUserID,
		UserID:            priorityUsers[1].ID,
		UserName:          priorityUsers[1].Name,
		StartAt:           time.Date(2026, 3, 19, 8, 0, 0, 0, time.Local),
		EndAt:             time.Date(2026, 3, 19, 9, 40, 0, 0, time.Local),
		LeaveType:         "PersonalLeave",
		Reason:            "priority test",
	}
	if err := fixture.db.Create(leave).Error; err != nil {
		t.Fatalf("create leave approval: %v", err)
	}

	userRepo := repository.NewUserRepository(fixture.db)
	courseRepo := repository.NewCourseRepository(fixture.db)
	scheduleSettingRepo := repository.NewScheduleSettingRepository(fixture.db)
	service := &AttendanceService{
		repo:              repository.NewAttendanceRepository(userRepo, courseRepo),
		leaveApprovalRepo: repository.NewLeaveApprovalRepository(fixture.db),
		userRepo:          userRepo,
		restDayRepo:       repository.NewUserRestDayRepository(fixture.db),
		dingMgr:           &DingTalkClientManager{},
		schedulePeriodSrv: NewSchedulePeriodService(
			repository.NewSchedulePeriodRepository(fixture.db, scheduleSettingRepo),
			scheduleSettingRepo,
			&config.Schedule{Periods: []config.Period{{Name: "P1", Start: "08:00", End: "09:40"}}},
		),
		scheduleCfg: config.Schedule{Periods: []config.Period{{Name: "P1", Start: "08:00", End: "09:40"}}},
		logger:      zap.NewNop().Sugar(),
	}

	resp, err := service.GetSlotAttendanceStatus(
		context.Background(),
		1,
		time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local),
		fixture.request.Week,
		fixture.request.Section,
		nil,
	)
	if err != nil {
		t.Fatalf("get slot attendance status: %v", err)
	}

	if got := slotAttendanceNames(resp.ShouldArrive); !slices.Equal(got, []string{"LateUser", "MissingUser", "OnTimeUser"}) {
		t.Fatalf("unexpected should_arrive users: got %v", got)
	}
	if got := slotAttendanceNames(resp.OnLeave); !slices.Equal(got, []string{"LeaveCourseUser"}) {
		t.Fatalf("unexpected on_leave users: got %v", got)
	}
	if got := slotAttendanceNames(resp.OnRestDay); !slices.Equal(got, []string{"RestDayCourseUser"}) {
		t.Fatalf("unexpected on_rest_day users: got %v", got)
	}
	if got := slotAttendanceNames(resp.HasCourse); !slices.Equal(got, []string{"HasCourseUser"}) {
		t.Fatalf("unexpected has_course users: got %v", got)
	}
}

func TestSlotAttendanceStatusIgnoresRestDayWhenRestDayAttendanceDisabled(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 10, 0, 0, time.Local),
	})

	dayOfWeek := 4
	if err := fixture.db.Create(&model.UserRestDay{
		TenantID:  fixtureTenantID,
		UserID:    fixture.users.onTime.ID,
		DayOfWeek: &dayOfWeek,
	}).Error; err != nil {
		t.Fatalf("create rest day: %v", err)
	}
	disableRestDayAttendanceForTest(t, fixture.db)

	userRepo := repository.NewUserRepository(fixture.db)
	courseRepo := repository.NewCourseRepository(fixture.db)
	scheduleSettingRepo := repository.NewScheduleSettingRepository(fixture.db)
	service := &AttendanceService{
		repo:              repository.NewAttendanceRepository(userRepo, courseRepo),
		leaveApprovalRepo: repository.NewLeaveApprovalRepository(fixture.db),
		userRepo:          userRepo,
		restDayRepo:       repository.NewUserRestDayRepository(fixture.db),
		dingMgr:           &DingTalkClientManager{},
		schedulePeriodSrv: NewSchedulePeriodService(
			repository.NewSchedulePeriodRepository(fixture.db, scheduleSettingRepo),
			scheduleSettingRepo,
			&config.Schedule{Periods: []config.Period{{Name: "P1", Start: "08:00", End: "09:40"}}},
		),
		scheduleCfg: config.Schedule{Periods: []config.Period{{Name: "P1", Start: "08:00", End: "09:40"}}},
		logger:      zap.NewNop().Sugar(),
	}

	resp, err := service.GetSlotAttendanceStatus(
		context.Background(),
		1,
		time.Date(2026, 3, 19, 0, 0, 0, 0, time.Local),
		fixture.request.Week,
		fixture.request.Section,
		nil,
	)
	if err != nil {
		t.Fatalf("get slot attendance status: %v", err)
	}

	if got := slotAttendanceNames(resp.OnRestDay); len(got) != 0 {
		t.Fatalf("expected on_rest_day empty when toggle disabled, got %v", got)
	}
	if got := slotAttendanceNames(resp.ShouldArrive); !slices.Equal(got, []string{"LateUser", "MissingUser", "OnTimeUser"}) {
		t.Fatalf("expected should_arrive to include rest-day user when toggle disabled, got %v", got)
	}
}

func TestWeekSlotsSummaryIgnoresRestDayWhenRestDayAttendanceDisabled(t *testing.T) {
	fixture := newAttendanceRealtimeFixture(t, attendanceRealtimeFixtureOptions{
		now: time.Date(2026, 3, 19, 8, 10, 0, 0, time.Local),
	})

	dayOfWeek := 4
	if err := fixture.db.Create(&model.UserRestDay{
		TenantID:  fixtureTenantID,
		UserID:    fixture.users.onTime.ID,
		DayOfWeek: &dayOfWeek,
	}).Error; err != nil {
		t.Fatalf("create rest day: %v", err)
	}
	disableRestDayAttendanceForTest(t, fixture.db)

	userRepo := repository.NewUserRepository(fixture.db)
	courseRepo := repository.NewCourseRepository(fixture.db)
	scheduleSettingRepo := repository.NewScheduleSettingRepository(fixture.db)
	service := &AttendanceService{
		repo:              repository.NewAttendanceRepository(userRepo, courseRepo),
		leaveApprovalRepo: repository.NewLeaveApprovalRepository(fixture.db),
		userRepo:          userRepo,
		restDayRepo:       repository.NewUserRestDayRepository(fixture.db),
		dingMgr:           &DingTalkClientManager{},
		schedulePeriodSrv: NewSchedulePeriodService(
			repository.NewSchedulePeriodRepository(fixture.db, scheduleSettingRepo),
			scheduleSettingRepo,
			&config.Schedule{Periods: []config.Period{{Name: "P1", Start: "08:00", End: "09:40"}}},
		),
		scheduleCfg: config.Schedule{Periods: []config.Period{{Name: "P1", Start: "08:00", End: "09:40"}}},
		logger:      zap.NewNop().Sugar(),
	}

	resp, err := service.GetWeekSlotsAttendanceSummary(
		context.Background(),
		1,
		3,
		time.Date(2026, 3, 16, 0, 0, 0, 0, time.Local),
		nil,
	)
	if err != nil {
		t.Fatalf("get week slots attendance summary: %v", err)
	}

	var thursdayFirstSlot *dto.SlotSummaryItem
	for i := range resp.Slots {
		if resp.Slots[i].DayOfWeek == 4 && resp.Slots[i].Section == 1 {
			thursdayFirstSlot = &resp.Slots[i]
			break
		}
	}
	if thursdayFirstSlot == nil {
		t.Fatalf("expected to find thursday section-1 summary")
	}
	if thursdayFirstSlot.OnRestDayCount != 0 {
		t.Fatalf("expected on_rest_day_count 0 when toggle disabled, got %d", thursdayFirstSlot.OnRestDayCount)
	}
	if thursdayFirstSlot.ShouldArriveCount != 3 {
		t.Fatalf("expected should_arrive_count 3 when toggle disabled, got %d", thursdayFirstSlot.ShouldArriveCount)
	}
}

func slotAttendanceNames(users []dto.CourseAttendanceUserItem) []string {
	names := make([]string, 0, len(users))
	for _, user := range users {
		names = append(names, user.Name)
	}
	slices.Sort(names)
	return names
}
