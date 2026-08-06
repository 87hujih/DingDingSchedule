package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	workflowRecoveryPollInterval = 30 * time.Second
	workflowRecoveryBatchSize    = 20
)

func (a *Agent) RecoverExpiredExecutions(ctx context.Context, limit int) (int, error) {
	if a == nil {
		return 0, errors.New("agent is nil")
	}
	store, ok := a.workflowStore.(WorkflowRecoveryStore)
	if !ok {
		return 0, nil
	}
	now := time.Now().UTC()
	candidates, err := store.ListRecoverableExecutions(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	var recoveryErrors []error
	for _, candidate := range candidates {
		if err := a.recoverWorkflowExecution(ctx, store, candidate, now); err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf(
				"recover tenant=%d conversation=%q actor=%d: %w",
				candidate.Key.TenantID,
				candidate.Key.ConversationID,
				candidate.Key.ActorUserID,
				err,
			))
			continue
		}
		completed++
	}
	return completed, errors.Join(recoveryErrors...)
}

func (a *Agent) recoverWorkflowExecution( //nolint:funlen // Recovery keeps the fenced takeover/result/finalize sequence auditable in one function.
	ctx context.Context,
	store WorkflowRecoveryStore,
	candidate RecoverableWorkflowExecution,
	now time.Time,
) error {
	if candidate.Workflow == nil || candidate.Workflow.Execution == nil {
		return errors.New("recoverable workflow has no execution")
	}
	execution := candidate.Workflow.Execution
	storeCtx := workflowStoreContext(ctx, candidate.Key)
	if execution.Status == WorkflowExecutionResultRecorded {
		if execution.Result == nil {
			return errors.New("result-recorded execution has no result")
		}
		return store.DeleteReservedExecution(
			storeCtx,
			candidate.Key,
			candidate.Workflow.Version,
			execution.Reservation.ExecutionToken,
			"recovery_finalize_recorded",
		)
	}
	if execution.Status != WorkflowExecutionExecuting &&
		execution.Status != WorkflowExecutionRecoveryRequired {
		return fmt.Errorf("unsupported recovery status %q", execution.Status)
	}
	if execution.Reservation.LeaseExpiresAt.After(now) {
		return ErrExecutionInProgress
	}

	token, err := newExecutionToken()
	if err != nil {
		return err
	}
	next := cloneReservedExecution(execution.Reservation)
	next.ExecutionToken = token
	next.AttemptRequestID = newProtocolLiveRequestID(now)
	next.StartedAt = now
	next.LeaseExpiresAt = now.Add(protocolWriteLeaseDuration)
	taken, err := store.TakeoverExpiredExecution(
		storeCtx,
		candidate.Key,
		candidate.Workflow.Version,
		execution.Reservation.ExecutionToken,
		next,
	)
	if err != nil {
		return err
	}

	request, err := operationRequestFromReservation(candidate.Key, taken.Execution.Reservation)
	if err != nil {
		return deferWorkflowRecovery(storeCtx, store, candidate.Key, taken, token, now, err)
	}
	executeCtx, cancel := context.WithTimeout(storeCtx, protocolWriteExecutorTimeout)
	result := a.operationExecutor().Execute(executeCtx, request)
	cancel()
	if result.Response.Kind != ResponseResult || !validWriteEffect(result.Effect) {
		return deferWorkflowRecovery(
			storeCtx,
			store,
			candidate.Key,
			taken,
			token,
			now,
			errors.New("recovered write did not return an authoritative result"),
		)
	}
	recorded, err := store.RecordExecutionResult(
		storeCtx,
		candidate.Key,
		taken.Version,
		token,
		PersistedExecutionResultV1{
			BusinessKey: request.IdempotencyKey,
			WriteEffect: result.Effect,
			CompletedAt: time.Now().UTC(),
		},
	)
	if err != nil {
		return deferWorkflowRecovery(storeCtx, store, candidate.Key, taken, token, now, err)
	}
	return store.DeleteReservedExecution(
		storeCtx,
		candidate.Key,
		recorded.Version,
		token,
		"recovery_completed",
	)
}

func deferWorkflowRecovery(
	ctx context.Context,
	store WorkflowRecoveryStore,
	key WorkflowKey,
	taken *VersionedWorkflow,
	token string,
	now time.Time,
	cause error,
) error {
	if taken == nil {
		return cause
	}
	_, markErr := store.MarkExecutionRecoveryRequired(
		ctx,
		key,
		taken.Version,
		token,
		now.Add(protocolWriteLeaseDuration),
	)
	if markErr != nil {
		return errors.Join(cause, fmt.Errorf("mark recovery required: %w", markErr))
	}
	return cause
}

func operationRequestFromReservation(
	key WorkflowKey,
	reservation ReservedExecutionV1,
) (OperationRequest, error) {
	params := make(map[string]TrustedParam, len(reservation.TrustedParams))
	for field, value := range reservation.TrustedParams {
		canonical, ok := canonicalTrustedParamValue(field, value)
		if !ok {
			return OperationRequest{}, fmt.Errorf("invalid recovered trusted parameter %q", field)
		}
		params[field] = TrustedParam{
			Field:    field,
			Value:    canonical,
			Source:   TrustedParamSource{Kind: TrustedParamSourceWorkflow, Resolver: "execution_recovery"},
			TenantID: key.TenantID,
		}
	}
	conversationID, ok := extractParamString(params, "conversation_id")
	if !ok || strings.TrimSpace(conversationID) != strings.TrimSpace(key.ConversationID) {
		return OperationRequest{}, errors.New("recovered conversation does not match workflow key")
	}
	request := OperationRequest{
		Operation:      reservation.Operation,
		TenantID:       key.TenantID,
		ActorUserID:    key.ActorUserID,
		ConversationID: key.ConversationID,
		TrustedParams:  params,
		IdempotencyKey: reservation.BusinessKey,
	}
	businessKey, err := subscriptionBusinessKeyForRequest(request)
	if err != nil {
		return OperationRequest{}, err
	}
	if businessKey != reservation.BusinessKey {
		return OperationRequest{}, errors.New("recovered business key does not match canonical request")
	}
	return request, nil
}

func (a *Agent) workflowRecoveryLoop() {
	if a != nil && a.recoveryDone != nil {
		defer close(a.recoveryDone)
	}
	if a == nil {
		return
	}
	if _, ok := a.workflowStore.(WorkflowRecoveryStore); !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-a.stopCleanup
		cancel()
	}()

	run := func() {
		_, err := a.RecoverExpiredExecutions(ctx, workflowRecoveryBatchSize)
		if err != nil && ctx.Err() == nil && a.deps.Logger != nil {
			a.deps.Logger.Warnw("恢复 Agent 写操作失败", "err", err)
		}
	}
	run()
	ticker := time.NewTicker(workflowRecoveryPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			run()
		case <-ctx.Done():
			return
		}
	}
}
