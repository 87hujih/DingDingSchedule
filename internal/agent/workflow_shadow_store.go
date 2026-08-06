package agent

import (
	"bytes"
	"context"
	"errors"
	"time"
)

const workflowShadowOperationTimeout = 250 * time.Millisecond

type WorkflowShadowEvent struct {
	Operation      string
	Code           string
	PrimaryVersion uint64
	MirrorVersion  uint64
}

type WorkflowShadowObserver func(WorkflowShadowEvent)

type workflowShadowStore struct {
	primary WorkflowStore
	mirror  WorkflowStore
	observe WorkflowShadowObserver
}

func NewWorkflowShadowStore(primary, mirror WorkflowStore, observe WorkflowShadowObserver) (WorkflowStore, error) {
	if primary == nil {
		return nil, errors.New("workflow shadow primary store is required")
	}
	if mirror == nil {
		return nil, errors.New("workflow shadow mirror store is required")
	}
	return &workflowShadowStore{primary: primary, mirror: mirror, observe: observe}, nil
}

func (s *workflowShadowStore) Load(ctx context.Context, key WorkflowKey) (*VersionedWorkflow, error) {
	current, err := s.primary.Load(ctx, key)
	if err != nil {
		return nil, err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	mirrored, mirrorErr := s.mirror.Load(mirrorCtx, key)
	cancel()
	if mirrorErr != nil {
		s.report("load", "shadow_error", current, nil)
		return current, nil
	}
	s.compare("load", current, mirrored)
	return current, nil
}

func (s *workflowShadowStore) Create(ctx context.Context, key WorkflowKey, next *WorkflowSnapshot) (*VersionedWorkflow, error) {
	current, err := s.primary.Create(ctx, key, next)
	if err != nil {
		return nil, err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	mirrored, mirrorErr := s.mirror.Create(mirrorCtx, key, current.Snapshot)
	cancel()
	s.afterMutation("create", current, mirrored, mirrorErr)
	return current, nil
}

func (s *workflowShadowStore) CompareAndSwap(ctx context.Context, key WorkflowKey, expectedVersion uint64, next *WorkflowSnapshot) (*VersionedWorkflow, error) {
	current, err := s.primary.CompareAndSwap(ctx, key, expectedVersion, next)
	if err != nil {
		return nil, err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	mirrored, mirrorErr := s.mirror.CompareAndSwap(mirrorCtx, key, expectedVersion, current.Snapshot)
	cancel()
	s.afterMutation("compare_and_swap", current, mirrored, mirrorErr)
	return current, nil
}

func (s *workflowShadowStore) DeleteIfVersion(ctx context.Context, key WorkflowKey, expectedVersion uint64, reason string) error {
	if err := s.primary.DeleteIfVersion(ctx, key, expectedVersion, reason); err != nil {
		return err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	err := s.mirror.DeleteIfVersion(mirrorCtx, key, expectedVersion, reason)
	cancel()
	if err != nil {
		s.report("delete", "shadow_error", nil, nil)
	}
	return nil
}

func (s *workflowShadowStore) CreateReservedExecution(
	ctx context.Context,
	key WorkflowKey,
	next *WorkflowSnapshot,
	reservation ReservedExecutionV1,
) (*VersionedWorkflow, error) {
	current, err := s.primary.CreateReservedExecution(ctx, key, next, reservation)
	if err != nil {
		return nil, err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	mirrored, mirrorErr := s.mirror.CreateReservedExecution(
		mirrorCtx,
		key,
		current.Snapshot,
		current.Execution.Reservation,
	)
	cancel()
	s.afterMutation("create_reserved", current, mirrored, mirrorErr)
	return current, nil
}

func (s *workflowShadowStore) ReserveExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	next *WorkflowSnapshot,
	reservation ReservedExecutionV1,
) (*VersionedWorkflow, error) {
	current, err := s.primary.ReserveExecution(ctx, key, expectedVersion, next, reservation)
	if err != nil {
		return nil, err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	mirrored, mirrorErr := s.mirror.ReserveExecution(
		mirrorCtx,
		key,
		expectedVersion,
		current.Snapshot,
		current.Execution.Reservation,
	)
	cancel()
	s.afterMutation("reserve", current, mirrored, mirrorErr)
	return current, nil
}

func (s *workflowShadowStore) RecordExecutionResult(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	result PersistedExecutionResultV1,
) (*VersionedWorkflow, error) {
	current, err := s.primary.RecordExecutionResult(ctx, key, expectedVersion, executionToken, result)
	if err != nil {
		return nil, err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	mirrored, mirrorErr := s.mirror.RecordExecutionResult(
		mirrorCtx,
		key,
		expectedVersion,
		executionToken,
		*current.Execution.Result,
	)
	cancel()
	s.afterMutation("record_result", current, mirrored, mirrorErr)
	return current, nil
}

func (s *workflowShadowStore) FinalizeExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	next *WorkflowSnapshot,
) (*VersionedWorkflow, error) {
	current, err := s.primary.FinalizeExecution(ctx, key, expectedVersion, executionToken, next)
	if err != nil {
		return nil, err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	mirrored, mirrorErr := s.mirror.FinalizeExecution(mirrorCtx, key, expectedVersion, executionToken, current.Snapshot)
	cancel()
	s.afterMutation("finalize", current, mirrored, mirrorErr)
	return current, nil
}

func (s *workflowShadowStore) DeleteReservedExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	reason string,
) error {
	if err := s.primary.DeleteReservedExecution(ctx, key, expectedVersion, executionToken, reason); err != nil {
		return err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	err := s.mirror.DeleteReservedExecution(mirrorCtx, key, expectedVersion, executionToken, reason)
	cancel()
	if err != nil {
		s.report("delete_reserved", "shadow_error", nil, nil)
	}
	return nil
}

func (s *workflowShadowStore) TakeoverExpiredExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	previousToken string,
	next ReservedExecutionV1,
) (*VersionedWorkflow, error) {
	current, err := s.primary.TakeoverExpiredExecution(ctx, key, expectedVersion, previousToken, next)
	if err != nil {
		return nil, err
	}
	mirrorCtx, cancel := context.WithTimeout(ctx, workflowShadowOperationTimeout)
	mirrored, mirrorErr := s.mirror.TakeoverExpiredExecution(
		mirrorCtx,
		key,
		expectedVersion,
		previousToken,
		current.Execution.Reservation,
	)
	cancel()
	s.afterMutation("takeover", current, mirrored, mirrorErr)
	return current, nil
}

func (s *workflowShadowStore) afterMutation(operation string, primary, mirror *VersionedWorkflow, mirrorErr error) {
	if mirrorErr != nil {
		s.report(operation, "shadow_error", primary, nil)
		return
	}
	s.compare(operation, primary, mirror)
}

func (s *workflowShadowStore) compare(operation string, primary, mirror *VersionedWorkflow) {
	if primary == nil || mirror == nil {
		if primary != nil || mirror != nil {
			s.report(operation, "presence_diff", primary, mirror)
		}
		return
	}
	if primary.Version != mirror.Version {
		s.report(operation, "version_diff", primary, mirror)
		return
	}
	primaryHash, primaryErr := WorkflowSnapshotHash(primary.Snapshot)
	mirrorHash, mirrorErr := WorkflowSnapshotHash(mirror.Snapshot)
	if primaryErr != nil || mirrorErr != nil {
		s.report(operation, "snapshot_hash_error", primary, mirror)
		return
	}
	if primaryHash != mirrorHash {
		s.report(operation, "snapshot_diff", primary, mirror)
		return
	}
	equal, err := canonicalExecutionEqual(primary.Execution, mirror.Execution)
	if err != nil {
		s.report(operation, "execution_hash_error", primary, mirror)
		return
	}
	if !equal {
		s.report(operation, "execution_diff", primary, mirror)
	}
}

func canonicalExecutionEqual(left, right *PersistedExecutionV1) (bool, error) {
	if left == nil || right == nil {
		return left == nil && right == nil, nil
	}
	if left.Status != right.Status {
		return false, nil
	}
	leftReservation, err := MarshalReservedExecution(left.Reservation)
	if err != nil {
		return false, err
	}
	rightReservation, err := MarshalReservedExecution(right.Reservation)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(leftReservation, rightReservation) {
		return false, nil
	}
	if left.Result == nil || right.Result == nil {
		return left.Result == nil && right.Result == nil, nil
	}
	leftResult, err := MarshalPersistedExecutionResult(*left.Result)
	if err != nil {
		return false, err
	}
	rightResult, err := MarshalPersistedExecutionResult(*right.Result)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftResult, rightResult), nil
}

func (s *workflowShadowStore) report(operation, code string, primary, mirror *VersionedWorkflow) {
	if s.observe == nil {
		return
	}
	event := WorkflowShadowEvent{Operation: operation, Code: code}
	if primary != nil {
		event.PrimaryVersion = primary.Version
	}
	if mirror != nil {
		event.MirrorVersion = mirror.Version
	}
	s.observe(event)
}
