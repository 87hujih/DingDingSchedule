package scheduler

import (
	"slices"
	"testing"
	"time"

	"schedule_server/config"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

func TestAttendanceSchedulerSchedulesFinalizeAtThirtyMinutes(t *testing.T) {
	scheduler := newAttendanceSchedulerTestSubject()

	scheduler.loadTenantScheduleFromConfig(1)

	entryIDs := scheduler.tenantJobs[1]
	if len(entryIDs) != 2 {
		t.Fatalf("expected 2 jobs for one section, got %d", len(entryIDs))
	}

	nextTimes := attendanceSchedulerNextTimes(t, scheduler, entryIDs, time.Date(2026, 3, 19, 7, 59, 0, 0, time.Local))
	want := []string{"08:02:00", "08:30:00"}
	if !slices.Equal(nextTimes, want) {
		t.Fatalf("unexpected scheduled times: got %v want %v", nextTimes, want)
	}
}

func TestAttendanceSchedulerKeepsPushFlowUnchanged(t *testing.T) {
	scheduler := newAttendanceSchedulerTestSubject()

	scheduler.loadTenantScheduleFromConfig(1)

	entryIDs := scheduler.tenantJobs[1]
	nextTimes := attendanceSchedulerNextTimes(t, scheduler, entryIDs, time.Date(2026, 3, 19, 7, 59, 0, 0, time.Local))
	if !slices.Contains(nextTimes, "08:02:00") {
		t.Fatalf("expected original push trigger at 08:02:00, got %v", nextTimes)
	}
}

func newAttendanceSchedulerTestSubject() *AttendanceScheduler {
	return &AttendanceScheduler{
		scheduleCfg: config.Schedule{
			Periods: []config.Period{
				{Name: "第一节", Start: "08:00", End: "09:40"},
			},
			TriggerDelayMinutes: 2,
		},
		delayAfterClassStart: 2,
		tenantJobs:           make(map[uint][]cron.EntryID),
		tenantPeriodKeys:     make(map[uint]string),
		cron:                 cron.New(cron.WithSeconds(), cron.WithLocation(time.Local)),
		logger:               zap.NewNop().Sugar(),
	}
}

func attendanceSchedulerNextTimes(t *testing.T, scheduler *AttendanceScheduler, entryIDs []cron.EntryID, base time.Time) []string {
	t.Helper()

	nextTimes := make([]string, 0, len(entryIDs))
	for _, entryID := range entryIDs {
		entry := scheduler.cron.Entry(entryID)
		if entry.ID == 0 {
			t.Fatalf("missing cron entry %d", entryID)
		}
		nextTimes = append(nextTimes, entry.Schedule.Next(base).Format("15:04:05"))
	}
	slices.Sort(nextTimes)
	return nextTimes
}
