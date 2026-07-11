package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

type protocolLiveExecutionCoordinator struct {
	store WorkflowStore
	clock func() time.Time
}

func newProtocolLiveExecutionCoordinator(store WorkflowStore, clock func() time.Time) protocolLiveExecutionCoordinator {
	if clock == nil {
		clock = time.Now
	}
	return protocolLiveExecutionCoordinator{store: store, clock: clock}
}

func (c protocolLiveExecutionCoordinator) Execute(ctx context.Context, req WorkflowExecutionRequest, executor protocolOperationExecutor) (WorkflowExecutionResult, error) {
	if c.store == nil || executor == nil {
		return WorkflowExecutionResult{}, fmt.Errorf("workflow execution coordinator unavailable")
	}
	now := c.clock()
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

	result := executor.Execute(ctx, req.Operation)
	var next *WorkflowSnapshot
	if result.Response.Kind != ResponseResult {
		next = cloneWorkflowSnapshot(req.Workflow)
	}
	if err := c.store.FinalizeExecution(ctx, req.Key, reserved.Version, token, next); err != nil {
		return WorkflowExecutionResult{OperationResult: result, Reserved: reserved}, err
	}
	return WorkflowExecutionResult{OperationResult: result, Reserved: reserved}, nil
}

func newExecutionToken() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return newProtocolLiveRequestID(time.Now())
}
