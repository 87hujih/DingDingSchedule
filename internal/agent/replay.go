package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
	"schedule_server/internal/model"

	"gorm.io/gorm"
)

type ReplayCase struct {
	ID                   string
	Question             string
	TenantID             uint
	UserID               uint
	UserRole             int
	UserName             string
	ConversationID       string
	ConversationType     string
	ProtocolMode         string
	IntentDraft          ProtocolDraft
	ActiveWorkflowBefore *WorkflowSnapshot
	Expected             ReplayExpected
	CreatedAt            time.Time
}

type ReplayExpected struct {
	Act              string
	Domain           string
	Operation        string
	ResponseKind     string
	BlockedReason    string
	FailureLayer     string
	LegacyCalled     bool
	WorkflowDecision string
}

type ReplayActual struct {
	Act              string
	Domain           string
	Operation        string
	ResponseKind     string
	BlockedReason    string
	FailureLayer     string
	LegacyCalled     bool
	WorkflowDecision string
}

type ReplayFieldMismatch struct {
	Field    string
	Expected string
	Actual   string
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
		ID:               id,
		Question:         row.Question,
		TenantID:         row.TenantID,
		UserID:           row.UserID,
		UserRole:         row.UserRole,
		UserName:         row.UserName,
		ConversationID:   row.ConversationID,
		ConversationType: row.ConvType,
		ProtocolMode:     row.ProtocolMode,
		IntentDraft:      replayIntentDraft(row),
		Expected: ReplayExpected{
			Act:              row.ProtocolAct,
			Domain:           row.ProtocolDomain,
			Operation:        row.ProtocolOperation,
			ResponseKind:     row.ResponseKind,
			BlockedReason:    firstNonEmpty(row.BlockedReason, row.ProtocolBlockedReason),
			FailureLayer:     row.FailureLayer,
			LegacyCalled:     row.LegacyCalled,
			WorkflowDecision: row.WorkflowDecision,
		},
		ActiveWorkflowBefore: replayWorkflowBefore(row),
		CreatedAt:            row.CreatedAt,
	}
}

func replayWorkflowBefore(row model.AgentCallLog) *WorkflowSnapshot {
	if workflow := workflowSnapshotFromReplayJSON(row.WorkflowSnapshotBeforeJSON); workflow != nil {
		overlayReplayWorkflowFields(workflow, row.WorkflowIDBefore, row.WorkflowTypeBefore, row.WorkflowStateBefore, row.TenantID, row.UserID, row.ConversationID)
		if workflow.Type == "" {
			workflow.Type = replayWorkflowTypeFromLog(row)
		}
		if len(workflowMissingFields(workflow)) == 0 {
			setWorkflowMissingFields(workflow, replayWorkflowMissingFieldsFromState(workflow.State))
		}
		return workflow
	}
	if strings.TrimSpace(row.WorkflowIDBefore) == "" && strings.TrimSpace(row.WorkflowStateBefore) == "" {
		return nil
	}
	workflow := &WorkflowSnapshot{
		ID:             row.WorkflowIDBefore,
		Type:           replayWorkflowTypeFromLog(row),
		State:          WorkflowState(row.WorkflowStateBefore),
		TenantID:       row.TenantID,
		ActorUserID:    row.UserID,
		ConversationID: row.ConversationID,
	}
	setWorkflowMissingFields(workflow, replayWorkflowMissingFieldsFromState(workflow.State))
	return workflow
}

func replayWorkflowTypeFromLog(row model.AgentCallLog) WorkflowType {
	if value := strings.TrimSpace(row.WorkflowTypeBefore); value != "" {
		return WorkflowType(value)
	}
	switch WorkflowState(strings.TrimSpace(row.WorkflowStateBefore)) {
	case WorkflowCollectScope, WorkflowCollectDepartments:
		return WorkflowSubscriptionStart
	case WorkflowCollectUser, WorkflowCollectDate, WorkflowCollectSection:
		return WorkflowManualSignCreate
	}
	if manifest, ok := lookupOperation(row.ProtocolOperation); ok && manifest.Workflow != nil {
		return manifest.Workflow.Type
	}
	return WorkflowType(strings.TrimSpace(row.ProtocolOperation))
}

func replayWorkflowMissingFieldsFromState(state WorkflowState) []string {
	switch state {
	case WorkflowCollectScope:
		return []string{"scope"}
	case WorkflowCollectDepartments:
		return []string{"dept_names"}
	case WorkflowCollectUser:
		return []string{"user_id"}
	case WorkflowCollectDate:
		return []string{"date"}
	case WorkflowCollectSection:
		return []string{"section"}
	default:
		return nil
	}
}

func replayIntentDraft(row model.AgentCallLog) ProtocolDraft {
	var draft ProtocolDraft
	if raw := strings.TrimSpace(row.IntentDraftJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &draft); err == nil && strings.TrimSpace(draft.Operation) != "" {
			return draft
		}
	}
	return ProtocolDraft{
		Act:       UserAct(row.ProtocolAct),
		Domain:    BusinessDomain(row.ProtocolDomain),
		Operation: row.ProtocolOperation,
	}
}

type replayWorkflowSnapshotJSON struct {
	ID             string                               `json:"id,omitempty"`
	Type           WorkflowType                         `json:"type,omitempty"`
	State          WorkflowState                        `json:"state,omitempty"`
	TenantID       uint                                 `json:"tenant_id,omitempty"`
	ActorUserID    uint                                 `json:"actor_user_id,omitempty"`
	ConversationID string                               `json:"conversation_id,omitempty"`
	MissingFields  []string                             `json:"missing_fields,omitempty"`
	Candidates     map[string][]replayWorkflowCandidate `json:"candidates,omitempty"`
}

type replayWorkflowCandidate struct {
	ID       string `json:"id,omitempty"`
	Label    string `json:"label,omitempty"`
	Value    any    `json:"value,omitempty"`
	TenantID uint   `json:"tenant_id,omitempty"`
}

func compactWorkflowSnapshotForReplay(workflow *WorkflowSnapshot) string {
	if workflow == nil {
		return ""
	}
	data, err := json.Marshal(replayWorkflowSnapshotJSON{
		ID:             workflow.ID,
		Type:           workflow.Type,
		State:          workflow.State,
		TenantID:       workflow.TenantID,
		ActorUserID:    workflow.ActorUserID,
		ConversationID: workflow.ConversationID,
		MissingFields:  cloneStringSlice(workflowMissingFields(workflow)),
		Candidates:     replayWorkflowCandidatesJSON(workflow.Candidates),
	})
	if err != nil {
		return ""
	}
	return string(data)
}

func workflowSnapshotFromReplayJSON(raw string) *WorkflowSnapshot {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var snapshot replayWorkflowSnapshotJSON
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil
	}
	workflow := &WorkflowSnapshot{
		ID:             snapshot.ID,
		Type:           snapshot.Type,
		State:          snapshot.State,
		TenantID:       snapshot.TenantID,
		ActorUserID:    snapshot.ActorUserID,
		ConversationID: snapshot.ConversationID,
		MissingFields:  cloneStringSlice(snapshot.MissingFields),
		MissingSlots:   cloneStringSlice(snapshot.MissingFields),
		Candidates:     replayWorkflowCandidatesFromJSON(snapshot.Candidates),
	}
	return workflow
}

func overlayReplayWorkflowFields(workflow *WorkflowSnapshot, id, workflowType, state string, tenantID, actorUserID uint, conversationID string) {
	if workflow == nil {
		return
	}
	if strings.TrimSpace(id) != "" {
		workflow.ID = strings.TrimSpace(id)
	}
	if strings.TrimSpace(workflowType) != "" {
		workflow.Type = WorkflowType(strings.TrimSpace(workflowType))
	}
	if strings.TrimSpace(state) != "" {
		workflow.State = WorkflowState(strings.TrimSpace(state))
	}
	if tenantID != 0 {
		workflow.TenantID = tenantID
	}
	if actorUserID != 0 {
		workflow.ActorUserID = actorUserID
	}
	if strings.TrimSpace(conversationID) != "" {
		workflow.ConversationID = strings.TrimSpace(conversationID)
	}
	setWorkflowMissingFields(workflow, workflowMissingFields(workflow))
}

func replayWorkflowCandidatesJSON(values map[string][]Candidate) map[string][]replayWorkflowCandidate {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string][]replayWorkflowCandidate, len(values))
	for field, candidates := range values {
		for _, candidate := range candidates {
			result[field] = append(result[field], replayWorkflowCandidate{
				ID:       candidate.ID,
				Label:    candidate.Label,
				Value:    candidate.Value,
				TenantID: candidate.TenantID,
			})
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func replayWorkflowCandidatesFromJSON(values map[string][]replayWorkflowCandidate) map[string][]Candidate {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string][]Candidate, len(values))
	for field, candidates := range values {
		for _, candidate := range candidates {
			result[field] = append(result[field], Candidate{
				ID:       candidate.ID,
				Label:    candidate.Label,
				Value:    candidate.Value,
				TenantID: candidate.TenantID,
			})
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
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
	AllowWrites             bool
	Compiler                IntentCompiler
	Schedule                SchedulePort
	Attendance              AttendancePort
	AttendanceUserDayStatus AttendanceUserDayStatusPort
	User                    UserPort
	Semester                SemesterPort
	SchedulePeriod          SchedulePeriodPort
	GroupSub                GroupSubPort
	Dept                    DeptPort
	Knowledge               KnowledgePort
	Clock                   func() time.Time
}

type ReplayRunner struct {
	options ReplayRunnerOptions
}

type ReplayStatus string

const (
	ReplayMatched      ReplayStatus = "matched"
	ReplaySkippedWrite ReplayStatus = "skipped_write"
	ReplayMismatched   ReplayStatus = "mismatched"
)

type ReplayResult struct {
	Status             ReplayStatus
	DryRun             bool
	RealWriteAttempted bool
	Actual             ReplayActual
	Mismatches         []ReplayFieldMismatch
}

func NewReplayRunner(options ReplayRunnerOptions) ReplayRunner {
	return ReplayRunner{options: options}
}

func (r ReplayRunner) RunCase(ctx context.Context, tc ReplayCase) ReplayResult {
	result := ReplayResult{DryRun: !r.options.AllowWrites}
	if normalizeProtocolMode(tc.ProtocolMode) == ProtocolModeLive && tc.Expected.LegacyCalled {
		result.Status = ReplayMismatched
		result.Actual = ReplayActual{LegacyCalled: true}
		result.Mismatches = append(result.Mismatches, ReplayFieldMismatch{
			Field:    "legacy_called",
			Expected: "false",
			Actual:   "true",
		})
		return result
	}

	writeAttempted := false
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler:       r.compiler(tc),
		Executor:       replayDryRunExecutor{delegate: r.operationExecutor(), allowWrites: r.options.AllowWrites, expected: tc.Expected, realWriteAttempted: &writeAttempted},
		User:           r.options.User,
		Dept:           r.options.Dept,
		Semester:       r.options.Semester,
		SchedulePeriod: r.options.SchedulePeriod,
		Clock:          r.options.Clock,
	})
	outcome := pipeline.Handle(ctx, protocolLiveInput{
		Message:        tc.Question,
		User:           replayUserContext(tc),
		ActiveWorkflow: tc.ActiveWorkflowBefore,
	})
	result.RealWriteAttempted = writeAttempted
	result.Actual = replayActualFromOutcome(outcome)
	result.Mismatches = compareReplayExpected(tc.Expected, result.Actual)
	if len(result.Mismatches) > 0 {
		result.Status = ReplayMismatched
		return result
	}
	result.Status = ReplayMatched
	return result
}

func (r ReplayRunner) compiler(tc ReplayCase) IntentCompiler {
	if r.options.Compiler != nil {
		return r.options.Compiler
	}
	return replayCaseCompiler{draft: tc.IntentDraft}
}

func (r ReplayRunner) operationExecutor() protocolOperationExecutor {
	return newOperationExecutor(operationExecutorDeps{
		Schedule:                r.options.Schedule,
		Attendance:              r.options.Attendance,
		AttendanceUserDayStatus: r.options.AttendanceUserDayStatus,
		Semester:                r.options.Semester,
		GroupSub:                r.options.GroupSub,
		Dept:                    r.options.Dept,
		Knowledge:               r.options.Knowledge,
	})
}

type replayCaseCompiler struct {
	draft ProtocolDraft
}

func (c replayCaseCompiler) Compile(context.Context, IntentCompileRequest) (IntentDraft, error) {
	if strings.TrimSpace(c.draft.Operation) == "" && c.draft.Act == "" {
		return unknownIntentDraft("intent_replay_missing_draft"), nil
	}
	return c.draft, nil
}

type replayDryRunExecutor struct {
	delegate           protocolOperationExecutor
	allowWrites        bool
	expected           ReplayExpected
	realWriteAttempted *bool
}

func (e replayDryRunExecutor) Execute(ctx context.Context, req OperationRequest) OperationExecutionResult {
	if manifest, ok := lookupOperation(req.Operation); ok && manifest.IsWrite {
		if !e.allowWrites {
			return operationExecutionResult(ResponseModel{
				Kind:    replayDryRunResponseKind(e.expected.ResponseKind),
				Payload: OperationStatusPayload{Code: "replay_dry_run"},
			}, answerModeToolFirst)
		}
		if e.realWriteAttempted != nil {
			*e.realWriteAttempted = true
		}
	}
	if e.delegate == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	return e.delegate.Execute(ctx, req)
}

func replayDryRunResponseKind(expected string) ResponseKind {
	switch kind := ResponseKind(strings.TrimSpace(expected)); kind {
	case ResponseAnswer, ResponseResult, ResponseClarify, ResponseSelectOptions, ResponseRefuse, ResponseConfirm:
		return kind
	default:
		return ResponseResult
	}
}

func replayUserContext(tc ReplayCase) *tools.UserContext {
	role := tc.UserRole
	if role == 0 && tc.Expected.BlockedReason != "role_denied" {
		if manifest, ok := lookupOperation(firstNonEmpty(tc.Expected.Operation, tc.IntentDraft.Operation)); ok && manifest.MinRole > role {
			role = manifest.MinRole
		}
	}
	return &tools.UserContext{
		TenantID:          tc.TenantID,
		UserID:            tc.UserID,
		UserRole:          role,
		Name:              tc.UserName,
		ConversationType:  tc.ConversationType,
		ConversationID:    tc.ConversationID,
		ConversationTitle: tc.ConversationID,
	}
}

func replayActualFromOutcome(outcome protocolLiveOutcome) ReplayActual {
	return ReplayActual{
		Act:              string(outcome.Draft.Act),
		Domain:           string(outcome.Draft.Domain),
		Operation:        outcome.Draft.Operation,
		ResponseKind:     string(outcome.Response.Kind),
		BlockedReason:    outcome.BlockedReason,
		FailureLayer:     string(outcome.FailureLayer),
		LegacyCalled:     outcome.LegacyCalled,
		WorkflowDecision: string(outcome.WorkflowDecision),
	}
}

func compareReplayExpected(expected ReplayExpected, actual ReplayActual) []ReplayFieldMismatch {
	var mismatches []ReplayFieldMismatch
	mismatches = appendReplayMismatch(mismatches, "act", expected.Act, actual.Act)
	mismatches = appendReplayMismatch(mismatches, "domain", expected.Domain, actual.Domain)
	mismatches = appendReplayMismatch(mismatches, "operation", expected.Operation, actual.Operation)
	mismatches = appendReplayMismatch(mismatches, "response_kind", expected.ResponseKind, actual.ResponseKind)
	mismatches = appendReplayMismatch(mismatches, "blocked_reason", expected.BlockedReason, actual.BlockedReason)
	mismatches = appendReplayMismatch(mismatches, "failure_layer", expected.FailureLayer, actual.FailureLayer)
	mismatches = appendReplayMismatch(mismatches, "legacy_called", replayBoolString(expected.LegacyCalled), replayBoolString(actual.LegacyCalled))
	mismatches = appendReplayMismatch(mismatches, "workflow_decision", expected.WorkflowDecision, actual.WorkflowDecision)
	return mismatches
}

func appendReplayMismatch(mismatches []ReplayFieldMismatch, field, expected, actual string) []ReplayFieldMismatch {
	if strings.TrimSpace(expected) == strings.TrimSpace(actual) {
		return mismatches
	}
	return append(mismatches, ReplayFieldMismatch{Field: field, Expected: expected, Actual: actual})
}

func replayBoolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
