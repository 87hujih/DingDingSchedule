package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

type ReplayCase struct {
	ID                   string
	Question             string
	TenantID             uint
	UserID               uint
	ConversationID       string
	ProtocolMode         string
	ActiveWorkflowBefore *WorkflowSnapshot
	Expected             ReplayExpected
	CreatedAt            time.Time
}

type ReplayExpected struct {
	Act           string
	Domain        string
	Operation     string
	ResponseKind  string
	BlockedReason string
	FailureLayer  string
	LegacyCalled  bool
}

type ReplayFilter struct {
	Since        time.Time
	Until        time.Time
	FailureLayer string
	Operation    string
	TenantID     uint
}

func ReplayCaseFromCallLog(row model.AgentCallLog) ReplayCase {
	id := strings.TrimSpace(row.ReplayCaseID)
	if id == "" {
		id = fmt.Sprintf("agent_call_log:%d", row.ID)
	}
	return ReplayCase{
		ID:             id,
		Question:       row.Question,
		TenantID:       row.TenantID,
		UserID:         row.UserID,
		ConversationID: row.ConversationID,
		ProtocolMode:   row.ProtocolMode,
		Expected: ReplayExpected{
			Act:           row.ProtocolAct,
			Domain:        row.ProtocolDomain,
			Operation:     row.ProtocolOperation,
			ResponseKind:  row.ResponseKind,
			BlockedReason: firstNonEmpty(row.BlockedReason, row.ProtocolBlockedReason),
			FailureLayer:  row.FailureLayer,
			LegacyCalled:  row.LegacyCalled,
		},
		ActiveWorkflowBefore: replayWorkflowBefore(row),
		CreatedAt:            row.CreatedAt,
	}
}

func replayWorkflowBefore(row model.AgentCallLog) *WorkflowSnapshot {
	if strings.TrimSpace(row.WorkflowIDBefore) == "" && strings.TrimSpace(row.WorkflowStateBefore) == "" {
		return nil
	}
	return &WorkflowSnapshot{
		ID:             row.WorkflowIDBefore,
		Type:           WorkflowType(row.ProtocolOperation),
		State:          WorkflowState(row.WorkflowStateBefore),
		TenantID:       row.TenantID,
		ActorUserID:    row.UserID,
		ConversationID: row.ConversationID,
	}
}

func LoadReplayCasesFromDB(ctx context.Context, db *gorm.DB, filter ReplayFilter, limit int) ([]ReplayCase, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	if limit <= 0 {
		limit = 100
	}
	query := db.WithContext(ctx).
		Where("protocol_mode = ?", string(ProtocolModeLive)).
		Order("created_at DESC").
		Limit(limit)
	if filter.TenantID != 0 {
		query = query.Where("tenant_id = ?", filter.TenantID)
	}
	if strings.TrimSpace(filter.Operation) != "" {
		query = query.Where("protocol_operation = ?", strings.TrimSpace(filter.Operation))
	}
	if strings.TrimSpace(filter.FailureLayer) != "" {
		query = query.Where("failure_layer = ?", strings.TrimSpace(filter.FailureLayer))
	}
	if !filter.Since.IsZero() {
		query = query.Where("created_at >= ?", filter.Since)
	}
	if !filter.Until.IsZero() {
		query = query.Where("created_at <= ?", filter.Until)
	}

	var rows []model.AgentCallLog
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	cases := make([]ReplayCase, 0, len(rows))
	for _, row := range rows {
		cases = append(cases, ReplayCaseFromCallLog(row))
	}
	return cases, nil
}

type FailureLayerReport struct {
	FailureLayerCounts   map[string]int
	OperationStats       map[string]OperationFailureStats
	CommonReasonCounts   map[string]int
	LegacyCalledCount    int
	CompilerTimeoutCount int
	InvalidJSONCount     int
}

type OperationFailureStats struct {
	Total       int
	Success     int
	Failed      int
	SuccessRate float64
}

func BuildFailureLayerReport(rows []model.AgentCallLog) FailureLayerReport {
	report := FailureLayerReport{
		FailureLayerCounts: make(map[string]int),
		OperationStats:     make(map[string]OperationFailureStats),
		CommonReasonCounts: make(map[string]int),
	}
	for _, row := range rows {
		layer := strings.TrimSpace(row.FailureLayer)
		if layer != "" {
			report.FailureLayerCounts[layer]++
		}
		if row.LegacyCalled {
			report.LegacyCalledCount++
		}
		switch strings.TrimSpace(row.CompilerStatus) {
		case "timeout":
			report.CompilerTimeoutCount++
		case "invalid_json":
			report.InvalidJSONCount++
		}
		for _, reason := range []string{row.BlockedReason, row.ProtocolBlockedReason, row.PrePolicyResult, row.ResourcePolicyResult} {
			reason = strings.TrimSpace(reason)
			if isCommonFailureReason(reason) {
				report.CommonReasonCounts[reason]++
			}
		}
		operation := strings.TrimSpace(row.ProtocolOperation)
		if operation == "" {
			continue
		}
		stats := report.OperationStats[operation]
		stats.Total++
		if strings.TrimSpace(row.FailureLayer) == "" && row.Status != "failed" && row.Status != "timeout" {
			stats.Success++
		} else {
			stats.Failed++
		}
		stats.SuccessRate = float64(stats.Success) / float64(stats.Total)
		report.OperationStats[operation] = stats
	}
	return report
}

func isCommonFailureReason(reason string) bool {
	switch reason {
	case "invalid_json", "entity_ambiguous", "policy_denied", "pre_policy_denied", "resource_policy_denied", "write_guard_blocked":
		return true
	default:
		return strings.Contains(reason, "denied") || strings.Contains(reason, "ambiguous")
	}
}

func (r FailureLayerReport) Text() string {
	var b strings.Builder
	b.WriteString("Failure layers:\n")
	for _, key := range sortedMapKeys(r.FailureLayerCounts) {
		b.WriteString(fmt.Sprintf("- %s: %d\n", key, r.FailureLayerCounts[key]))
	}
	b.WriteString("Operation success rates:\n")
	for _, key := range sortedMapKeys(r.OperationStats) {
		stats := r.OperationStats[key]
		b.WriteString(fmt.Sprintf("- %s: %.2f (%d/%d)\n", key, stats.SuccessRate, stats.Success, stats.Total))
	}
	return b.String()
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type ReplayRunnerOptions struct {
	AllowWrites bool
}

type ReplayRunner struct {
	options ReplayRunnerOptions
}

type ReplayStatus string

const (
	ReplayMatched      ReplayStatus = "matched"
	ReplaySkippedWrite ReplayStatus = "skipped_write"
)

type ReplayResult struct {
	Status             ReplayStatus
	DryRun             bool
	RealWriteAttempted bool
}

func NewReplayRunner(options ReplayRunnerOptions) ReplayRunner {
	return ReplayRunner{options: options}
}

func (r ReplayRunner) RunCase(_ context.Context, tc ReplayCase) ReplayResult {
	manifest, ok := lookupOperation(tc.Expected.Operation)
	if ok && manifest.IsWrite && !r.options.AllowWrites {
		return ReplayResult{
			Status: ReplaySkippedWrite,
			DryRun: true,
		}
	}
	return ReplayResult{Status: ReplayMatched, DryRun: !r.options.AllowWrites}
}
