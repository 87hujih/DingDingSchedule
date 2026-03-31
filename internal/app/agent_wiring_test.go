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

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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

func TestAttendanceAdapterSignForUsersBySlotUsesDateAndSection(t *testing.T) {
	service := &fakeAttendanceDetailService{}
	adapter := &attendanceAdapter{srv: service}

	err := adapter.SignForUsersBySlot(context.Background(), "2026-03-25", 1, []uint{9})
	if err != nil {
		t.Fatalf("SignForUsersBySlot() error = %v", err)
	}

	if service.lastSignReq == nil {
		t.Fatalf("service sign request was not captured")
	}
	if service.lastSignReq.RecordID != 0 {
		t.Fatalf("RecordID = %d, want 0", service.lastSignReq.RecordID)
	}
	if service.lastSignReq.Date != "2026-03-25" {
		t.Fatalf("Date = %q, want 2026-03-25", service.lastSignReq.Date)
	}
	if service.lastSignReq.Section != 1 {
		t.Fatalf("Section = %d, want 1", service.lastSignReq.Section)
	}
	if !slices.Equal(service.lastSignReq.TargetUserIDs, []uint{9}) {
		t.Fatalf("TargetUserIDs = %v, want [9]", service.lastSignReq.TargetUserIDs)
	}
}

func TestCallLogAdapterPersistsDomainModeAndRetrievalDetails(t *testing.T) {
	db := newCallLogTestDB(t)
	adapter := &callLogAdapter{db: db}

	adapter.Write(context.Background(), agenttool.CallLog{
		TenantID:                1,
		UserID:                  7,
		UserName:                "Alice",
		ConvType:                "1",
		QueryType:               "rag",
		DomainResult:            "in_domain",
		AnswerMode:              "knowledge-only",
		Question:                "如果请假信息没能同步到位，会出现什么情况",
		ToolsCalled:             []string{"get_current_time"},
		ToolCallCount:           1,
		Reply:                   "同步失败不会直接覆盖已生成快照。",
		SourceRefs:              []string{"请假同步说明#3"},
		RetrievalHitCount:       1,
		RetrievalCandidateCount: 3,
		RetrievalTopRefs:        []string{"请假同步说明#3", "系统总览#1"},
		RetrievalScores:         []int{18, 9},
		RetrievalFilteredReason: "no_hits",
		KnowledgeDocTypes:       []string{"rule", "overview"},
		RetrievalDurationMs:     12,
		LLMDurationMs:           34,
		Rounds:                  1,
		DurationMs:              56,
		Status:                  "success",
	})

	var row model.AgentCallLog
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("query call log: %v", err)
	}
	if row.DomainResult != "in_domain" {
		t.Fatalf("DomainResult = %q, want in_domain", row.DomainResult)
	}
	if row.AnswerMode != "knowledge-only" {
		t.Fatalf("AnswerMode = %q, want knowledge-only", row.AnswerMode)
	}
	if row.RetrievalCandidateCount != 3 {
		t.Fatalf("RetrievalCandidateCount = %d, want 3", row.RetrievalCandidateCount)
	}
	if row.RetrievalTopRefs != "请假同步说明#3,系统总览#1" {
		t.Fatalf("RetrievalTopRefs = %q, want %q", row.RetrievalTopRefs, "请假同步说明#3,系统总览#1")
	}
	if row.RetrievalScores != "18,9" {
		t.Fatalf("RetrievalScores = %q, want 18,9", row.RetrievalScores)
	}
	if row.RetrievalFilteredReason != "no_hits" {
		t.Fatalf("RetrievalFilteredReason = %q, want no_hits", row.RetrievalFilteredReason)
	}
	if row.KnowledgeDocTypes != "rule,overview" {
		t.Fatalf("KnowledgeDocTypes = %q, want rule,overview", row.KnowledgeDocTypes)
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
	lastSignReq *dto.SignForUserRequest
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

func (f *fakeAttendanceDetailService) SignForUsers(_ context.Context, req *dto.SignForUserRequest) (*dto.SignForUserResponse, error) {
	if req != nil {
		copied := *req
		f.lastSignReq = &copied
	}
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

func newCallLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := "file:agent-call-log-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.AgentCallLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}
