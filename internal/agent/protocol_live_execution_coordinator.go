package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type WorkflowExecutionRequest struct {
	Key             WorkflowKey
	ExpectedVersion uint64
	Workflow        *WorkflowSnapshot
	Operation       OperationRequest
	BusinessKey     string
	RequestID       string
}

type WorkflowExecutionResult struct {
	OperationResult OperationExecutionResult
	Reserved        *VersionedWorkflow
}

type RecoveredOperationResult struct {
	Operation   string
	WriteEffect string
	PushEnabled *bool
}

type OperationExecutionLedger interface {
	FindSucceeded(context.Context, uint, string) (*RecoveredOperationResult, error)
}

type WorkflowOperationExecutor interface {
	Execute(context.Context, OperationRequest) OperationExecutionResult
}

// ExecuteWorkflowOperation runs a write through the durable lease/ledger
// coordinator. It is exported so composition-root integration tests can drive
// the same recovery boundary used by the live pipeline.
func ExecuteWorkflowOperation(ctx context.Context, store WorkflowStore, ledger OperationExecutionLedger, clock func() time.Time, req WorkflowExecutionRequest, executor WorkflowOperationExecutor) (WorkflowExecutionResult, error) {
	return newProtocolLiveExecutionCoordinator(store, ledger, clock).Execute(ctx, req, executor)
}

const (
	processingReply       = "操作正在处理中，请稍后再试。"
	recoveryRequiredReply = "操作结果需要人工确认，请勿重复操作。"
)

var ErrOperationLedgerLookup = errors.New("operation ledger lookup failed")

type protocolLiveExecutionCoordinator struct {
	store  WorkflowStore
	ledger OperationExecutionLedger
	clock  func() time.Time
}

func newProtocolLiveExecutionCoordinator(store WorkflowStore, ledger OperationExecutionLedger, clock func() time.Time) protocolLiveExecutionCoordinator {
	if clock == nil {
		clock = time.Now
	}
	return protocolLiveExecutionCoordinator{store: store, ledger: ledger, clock: clock}
}

func (c protocolLiveExecutionCoordinator) Execute(ctx context.Context, req WorkflowExecutionRequest, executor WorkflowOperationExecutor) (WorkflowExecutionResult, error) {
	if c.store == nil || executor == nil {
		return WorkflowExecutionResult{}, fmt.Errorf("workflow execution coordinator unavailable")
	}
	now := c.clock()
	recovered, err := c.findSucceeded(ctx, req)
	if err != nil {
		return WorkflowExecutionResult{}, fmt.Errorf("%w: %v", ErrOperationLedgerLookup, err)
	}
	if recovered != nil && req.Workflow != nil && req.Workflow.State == WorkflowExecuting && req.Workflow.ExecutionLease != nil {
		// The durable ledger proves the business effect completed. Only the
		// execution that owns the original version+token may clear its workflow;
		// a conflict means a newer owner exists and must be left untouched.
		if finalizeErr := c.store.FinalizeExecution(ctx, req.Key, req.ExpectedVersion, req.Workflow.ExecutionLease.ExecutionToken, nil); finalizeErr != nil && !errors.Is(finalizeErr, ErrWorkflowConflict) {
			return WorkflowExecutionResult{}, finalizeErr
		}
		return WorkflowExecutionResult{OperationResult: recoveredExecutionResult(recovered)}, nil
	}
	token := newExecutionToken()
	reserved, err := c.store.ReserveExecution(ctx, req.Key, req.ExpectedVersion, req.Workflow, WorkflowExecutionLease{
		ExecutionToken: token,
		Operation:      req.Operation.Operation,
		BusinessKey:    req.BusinessKey,
		RequestID:      req.RequestID,
		StartedAt:      now,
		LeaseExpiresAt: now.Add(WorkflowExecutionLeaseDuration),
	})
	if err != nil {
		return WorkflowExecutionResult{}, err
	}
	if recovered != nil {
		result := recoveredExecutionResult(recovered)
		if err := c.store.FinalizeExecution(ctx, req.Key, reserved.Version, token, nil); err != nil {
			if recoveredAgain, recoverErr := c.recoverAfterFinalizeConflict(ctx, req); recoverErr == nil && recoveredAgain != nil {
				return WorkflowExecutionResult{OperationResult: recoveredExecutionResult(recoveredAgain), Reserved: reserved}, nil
			}
			return WorkflowExecutionResult{OperationResult: result, Reserved: reserved}, err
		}
		return WorkflowExecutionResult{OperationResult: result, Reserved: reserved}, nil
	}
	if req.Workflow != nil && req.Workflow.State == WorkflowExecuting && req.Workflow.ExecutionLease != nil && !req.Workflow.ExecutionLease.LeaseExpiresAt.After(now) {
		next := cloneWorkflowSnapshot(req.Workflow)
		next.State = WorkflowRecoveryRequired
		result := OperationExecutionResult{Response: ResponseModel{Kind: ResponseRefuse, RefusalReason: recoveryRequiredReply}}
		if err := c.store.FinalizeExecution(ctx, req.Key, reserved.Version, token, next); err != nil {
			return WorkflowExecutionResult{OperationResult: result, Reserved: reserved}, err
		}
		return WorkflowExecutionResult{OperationResult: result, Reserved: reserved}, nil
	}

	result := executor.Execute(ctx, req.Operation)
	var next *WorkflowSnapshot
	if result.Response.Kind != ResponseResult {
		next = cloneWorkflowSnapshot(req.Workflow)
	}
	if err := c.store.FinalizeExecution(ctx, req.Key, reserved.Version, token, next); err != nil {
		if recovered, recoverErr := c.recoverAfterFinalizeConflict(ctx, req); recoverErr == nil && recovered != nil {
			return WorkflowExecutionResult{OperationResult: recoveredExecutionResult(recovered), Reserved: reserved}, nil
		}
		return WorkflowExecutionResult{OperationResult: result, Reserved: reserved}, err
	}
	return WorkflowExecutionResult{OperationResult: result, Reserved: reserved}, nil
}

func (c protocolLiveExecutionCoordinator) recoverAfterFinalizeConflict(ctx context.Context, req WorkflowExecutionRequest) (*RecoveredOperationResult, error) {
	return c.findSucceeded(ctx, req)
}

func (c protocolLiveExecutionCoordinator) findSucceeded(ctx context.Context, req WorkflowExecutionRequest) (*RecoveredOperationResult, error) {
	if c.ledger == nil || req.BusinessKey == "" {
		return nil, nil
	}
	return c.ledger.FindSucceeded(ctx, req.Key.TenantID, req.BusinessKey)
}

func recoveredExecutionResult(recovered *RecoveredOperationResult) OperationExecutionResult {
	code := "subscription_started"
	if recovered.Operation == "subscription.cancel" {
		code = "subscription_cancelled"
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, Payload: OperationStatusPayload{Code: code, Status: WriteStatus(recovered.WriteEffect), PushEnabled: recovered.PushEnabled}}, answerModeToolFirst)
}

func newExecutionToken() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return newProtocolLiveRequestID(time.Now())
}
