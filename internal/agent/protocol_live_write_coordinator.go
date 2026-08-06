package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	protocolWriteLeaseDuration    = 2 * time.Minute
	protocolWriteExecutorTimeout  = 90 * time.Second
	protocolWriteExecutionIDBytes = 24
)

func (a *Agent) coordinatePreparedWrite(
	ctx context.Context,
	key WorkflowKey,
	current *VersionedWorkflow,
	outcome protocolLiveOutcome,
) (protocolLiveOutcome, *VersionedWorkflow, bool, error) {
	if a == nil || a.workflowStore == nil || outcome.PreparedWrite == nil {
		return outcome, current, false, errors.New("prepared write coordinator is unavailable")
	}
	if current != nil && current.Execution != nil {
		return workflowExecutionPendingOutcome(outcome), current, false, ErrExecutionInProgress
	}

	now := time.Now().UTC()
	token, err := newExecutionToken()
	if err != nil {
		return outcome, current, false, err
	}
	snapshot, err := reservationSnapshot(outcome, key, now)
	if err != nil {
		return outcome, current, false, err
	}
	params, err := persistedTrustedParams(outcome.PreparedWrite.Request.TrustedParams)
	if err != nil {
		return outcome, current, false, err
	}
	reservation := ReservedExecutionV1{
		Operation:        outcome.PreparedWrite.Request.Operation,
		BusinessKey:      outcome.PreparedWrite.BusinessKey,
		TrustedParams:    params,
		ExecutionToken:   token,
		AttemptRequestID: outcome.RequestID,
		StartedAt:        now,
		LeaseExpiresAt:   now.Add(protocolWriteLeaseDuration),
	}

	storeCtx := workflowStoreContext(ctx, key)
	var reserved *VersionedWorkflow
	if current == nil {
		reserved, err = a.workflowStore.CreateReservedExecution(storeCtx, key, snapshot, reservation)
	} else {
		reserved, err = a.workflowStore.ReserveExecution(storeCtx, key, current.Version, snapshot, reservation)
	}
	if err != nil {
		return outcome, current, false, err
	}

	executeCtx, cancel := context.WithTimeout(ctx, protocolWriteExecutorTimeout)
	executionResult := a.operationExecutor().Execute(executeCtx, outcome.PreparedWrite.Request)
	cancel()
	setProtocolOutcomeResponse(&outcome, executionResult.Response, executionResult.Metrics.AnswerMode)
	outcome.ExecutionMetrics = executionResult.Metrics
	outcome.ExecutorStatus = protocolExecutorStatus(executionResult.Response)
	if executionResult.Response.Kind != ResponseResult || !validWriteEffect(executionResult.Effect) {
		return workflowExecutionPendingOutcome(outcome), reserved, true, errors.New("write execution result is not authoritative")
	}

	recorded, err := a.workflowStore.RecordExecutionResult(storeCtx, key, reserved.Version, token, PersistedExecutionResultV1{
		BusinessKey: outcome.PreparedWrite.BusinessKey,
		WriteEffect: executionResult.Effect,
		CompletedAt: time.Now().UTC(),
	})
	if err != nil {
		return workflowExecutionPendingOutcome(outcome), reserved, true, err
	}
	if err := a.workflowStore.DeleteReservedExecution(
		storeCtx,
		key,
		recorded.Version,
		token,
		string(WorkflowCompletedDecision),
	); err != nil {
		return workflowExecutionPendingOutcome(outcome), recorded, true, err
	}

	outcome.WorkflowDecision = WorkflowCompletedDecision
	outcome.ClearWorkflow = true
	outcome.WorkflowAfter = nil
	outcome.PreparedWrite = nil
	return outcome, nil, true, nil
}

func reservationSnapshot(outcome protocolLiveOutcome, key WorkflowKey, now time.Time) (*WorkflowSnapshot, error) {
	if outcome.PreparedWrite == nil {
		return nil, errors.New("prepared write is required")
	}
	if outcome.WorkflowAfter != nil {
		return cloneWorkflowSnapshot(outcome.WorkflowAfter), nil
	}
	if outcome.PreparedWrite.Request.Operation != "subscription.cancel" {
		return nil, errors.New("write operation requires a workflow snapshot")
	}
	return &WorkflowSnapshot{
		ID:             "execution-" + outcome.RequestID,
		TenantID:       key.TenantID,
		ActorUserID:    key.ActorUserID,
		ConversationID: key.ConversationID,
		Type:           WorkflowSubscriptionCancel,
		State:          WorkflowReady,
		ExpiresAt:      now.Add(protocolWriteLeaseDuration),
	}, nil
}

func persistedTrustedParams(params map[string]TrustedParam) (PersistedTrustedParamsV1, error) {
	if len(params) == 0 {
		return nil, nil
	}
	result := make(PersistedTrustedParamsV1, len(params))
	for field, param := range params {
		value, ok := canonicalTrustedParamValue(field, param.Value)
		if !ok {
			return nil, fmt.Errorf("trusted parameter %q is not persistable", field)
		}
		result[field] = value
	}
	return result, nil
}

func newExecutionToken() (string, error) {
	raw := make([]byte, protocolWriteExecutionIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate execution token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func validWriteEffect(effect WriteEffect) bool {
	switch effect {
	case WriteEffectCreated, WriteEffectUpdated, WriteEffectNoOp, WriteEffectCancelled:
		return true
	default:
		return false
	}
}

func workflowExecutionPendingOutcome(outcome protocolLiveOutcome) protocolLiveOutcome {
	outcome.PreparedWrite = nil
	outcome.BlockedReason = "write_result_pending"
	outcome.ExecutorStatus = "pending"
	outcome.FailureLayer = FailureExecutor
	setProtocolOutcomeResponse(&outcome, ResponseModel{
		Kind:          ResponseRefuse,
		RefusalReason: "操作结果仍在确认中，请稍后查询当前订阅状态，不要重复提交。",
	}, answerModeReject)
	return outcome
}

func workflowConflictRetryOutcome() protocolLiveOutcome {
	outcome := workflowStoreFailureOutcome()
	outcome.BlockedReason = "workflow_conflict_retry"
	setProtocolOutcomeResponse(&outcome, ResponseModel{
		Kind:          ResponseRefuse,
		RefusalReason: "当前会话状态刚刚发生变化，请重新发送一次请求。",
	}, answerModeReject)
	return outcome
}

func workflowHasActiveExecution(versioned *VersionedWorkflow) bool {
	return versioned != nil && versioned.Execution != nil &&
		strings.TrimSpace(versioned.Execution.Reservation.ExecutionToken) != ""
}
