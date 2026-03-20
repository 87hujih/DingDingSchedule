package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	agenttool "schedule_server/internal/agent/tools"
	"schedule_server/internal/dto"
	"schedule_server/internal/model"
)

func TestAttendanceAdapterUsesRealtimeViewForCurrentSlot(t *testing.T) {
	service := &fakeAttendanceDetailService{
		detailResp: &dto.AttendanceDetailResponse{
			Date:        "2026-03-19",
			Week:        3,
			Section:     1,
			ViewMode:    "current",
			IsFinalized: false,
			FinalizeAt:  time.Date(2026, 3, 19, 8, 30, 0, 0, time.Local),
			SlotTime: dto.SlotTimeInfo{
				Start: "08:00",
				End:   "09:40",
			},
			Statistics: dto.AttendanceStatistics{
				ShouldAttend: 3,
				OnTime:       1,
				Late:         1,
				NotArrived:   1,
			},
			Users: dto.AttendanceUserLists{
				OnTime:     []dto.AttendanceUserCheck{{Name: "OnTimeUser"}},
				Late:       []dto.AttendanceUserCheck{{Name: "LateUser"}},
				NotArrived: []dto.AttendanceUserBasic{{Name: "MissingUser"}},
			},
		},
	}

	adapter := &attendanceAdapter{srv: service}
	result, err := adapter.GetAttendanceDetail(context.Background(), agenttool.AttendanceQuery{
		Date:    "2026-03-19",
		Week:    3,
		Section: 1,
	})
	if err != nil {
		t.Fatalf("GetAttendanceDetail() error = %v", err)
	}

	if service.detailCalls != 1 {
		t.Fatalf("service detail call count = %d, want 1", service.detailCalls)
	}
	if result.ViewMode != "current" {
		t.Fatalf("ViewMode = %q, want current", result.ViewMode)
	}
	if result.IsFinalized {
		t.Fatalf("IsFinalized = true, want false")
	}
	if result.LateCount != 1 {
		t.Fatalf("LateCount = %d, want 1", result.LateCount)
	}
	if !slices.Equal(result.LateUsers, []string{"LateUser"}) {
		t.Fatalf("LateUsers = %v, want [LateUser]", result.LateUsers)
	}
}

func TestAttendanceAdapterUsesSnapshotForHistoryQueries(t *testing.T) {
	service := &fakeAttendanceDetailService{
		detailResp: &dto.AttendanceDetailResponse{
			Date:        "2026-03-18",
			Week:        3,
			Section:     1,
			ViewMode:    "final",
			IsFinalized: true,
			FinalizeAt:  time.Date(2026, 3, 18, 8, 30, 0, 0, time.Local),
			SlotTime: dto.SlotTimeInfo{
				Start: "08:00",
				End:   "09:40",
			},
			Statistics: dto.AttendanceStatistics{
				ShouldAttend: 2,
				OnTime:       1,
				Late:         1,
				NotArrived:   0,
			},
			Users: dto.AttendanceUserLists{
				OnTime: []dto.AttendanceUserCheck{{Name: "OnTimeUser"}},
				Late:   []dto.AttendanceUserCheck{{Name: "LateUser"}},
			},
		},
	}

	adapter := &attendanceAdapter{srv: service}
	result, err := adapter.GetAttendanceDetail(context.Background(), agenttool.AttendanceQuery{
		Date:    "2026-03-18",
		Week:    3,
		Section: 1,
	})
	if err != nil {
		t.Fatalf("GetAttendanceDetail() error = %v", err)
	}

	if service.detailCalls != 1 {
		t.Fatalf("service detail call count = %d, want 1", service.detailCalls)
	}
	if result.ViewMode != "final" {
		t.Fatalf("ViewMode = %q, want final", result.ViewMode)
	}
	if !result.IsFinalized {
		t.Fatalf("IsFinalized = false, want true")
	}
	if result.FinalizeAt != "2026-03-18 08:30:00" {
		t.Fatalf("FinalizeAt = %q, want 2026-03-18 08:30:00", result.FinalizeAt)
	}
}

type fakeAttendanceDetailService struct {
	detailResp  *dto.AttendanceDetailResponse
	detailErr   error
	textResp    *dto.AttendanceTextResponse
	textErr     error
	rankingResp *dto.WeeklyAttendanceRankingResponse
	rankingErr  error
	rateResp    *dto.WeeklyAttendanceRateRankingResponse
	rateErr     error
	signErr     error
	detailCalls int
	lastReq     *dto.AttendanceDetailRequest
}

func (f *fakeAttendanceDetailService) GetAttendanceDetail(_ context.Context, req *dto.AttendanceDetailRequest) (*dto.AttendanceDetailResponse, error) {
	f.detailCalls++
	if req != nil {
		copied := *req
		f.lastReq = &copied
	}
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	return f.detailResp, nil
}

func (f *fakeAttendanceDetailService) GetAttendanceText(context.Context, *dto.AttendanceTextRequest) (*dto.AttendanceTextResponse, error) {
	if f.textErr != nil {
		return nil, f.textErr
	}
	return f.textResp, nil
}

func (f *fakeAttendanceDetailService) GetWeeklyRanking(context.Context, *dto.WeeklyAttendanceRankingRequest) (*dto.WeeklyAttendanceRankingResponse, error) {
	if f.rankingErr != nil {
		return nil, f.rankingErr
	}
	return f.rankingResp, nil
}

func (f *fakeAttendanceDetailService) GetWeeklyAttendanceRateRanking(context.Context, *dto.WeeklyAttendanceRankingRequest) (*dto.WeeklyAttendanceRateRankingResponse, error) {
	if f.rateErr != nil {
		return nil, f.rateErr
	}
	return f.rateResp, nil
}

func (f *fakeAttendanceDetailService) SignForUsers(context.Context, *dto.SignForUserRequest) (*dto.SignForUserResponse, error) {
	if f.signErr != nil {
		return nil, f.signErr
	}
	return &dto.SignForUserResponse{}, nil
}

type fakeAttendanceRecordRepo struct {
	record *model.AttendanceRecord
	err    error
}

func (f *fakeAttendanceRecordRepo) Upsert(context.Context, *model.AttendanceRecord) error {
	return errors.New("unexpected call")
}
func (f *fakeAttendanceRecordRepo) FindByDateSection(context.Context, time.Time, int) (*model.AttendanceRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.record, nil
}
func (f *fakeAttendanceRecordRepo) ListByDate(context.Context, time.Time) ([]model.AttendanceRecord, error) {
	return nil, errors.New("unexpected call")
}
func (f *fakeAttendanceRecordRepo) ListByDateRange(context.Context, time.Time, time.Time) ([]model.AttendanceRecord, error) {
	return nil, errors.New("unexpected call")
}
func (f *fakeAttendanceRecordRepo) FindLatest(context.Context) (*model.AttendanceRecord, error) {
	return nil, errors.New("unexpected call")
}
func (f *fakeAttendanceRecordRepo) FindByID(context.Context, uint) (*model.AttendanceRecord, error) {
	return nil, errors.New("unexpected call")
}
