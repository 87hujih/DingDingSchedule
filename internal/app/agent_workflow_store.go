package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"schedule_server/internal/agent"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
)

const workflowCodecVersion uint16 = 1

type agentWorkflowStore struct {
	repo                   repository.AgentWorkflowRepository
	clock                  func() time.Time
	recoveryDecodeObserver func(model.AgentWorkflow, error)
}

func newAgentWorkflowStore(repo repository.AgentWorkflowRepository) (agent.WorkflowStore, error) {
	if repo == nil {
		return nil, errors.New("agent workflow repository is required")
	}
	return &agentWorkflowStore{repo: repo, clock: time.Now}, nil
}

func (s *agentWorkflowStore) Load(ctx context.Context, key agent.WorkflowKey) (*agent.VersionedWorkflow, error) {
	row, err := s.repo.Load(ctx, repositoryWorkflowKey(key))
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	if row.ExecutionStatus == repository.AgentWorkflowExecutionIdle && !row.ExpiresAt.After(s.now()) {
		if err := s.repo.DeleteIfVersion(ctx, repositoryWorkflowKey(key), row.Version); err != nil {
			return nil, mapWorkflowRepositoryError(err)
		}
		return nil, nil
	}
	return decodeAgentWorkflowRow(row)
}

func (s *agentWorkflowStore) ListRecoverableExecutions(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]agent.RecoverableWorkflowExecution, error) {
	rows, err := s.repo.ListRecoverableExecutions(ctx, now, limit)
	if err != nil {
		return nil, mapWorkflowRepositoryError(err)
	}
	result := make([]agent.RecoverableWorkflowExecution, 0, len(rows))
	for i := range rows {
		workflow, err := decodeAgentWorkflowRow(&rows[i])
		if err != nil {
			if s.recoveryDecodeObserver != nil {
				s.recoveryDecodeObserver(rows[i], err)
			}
			continue
		}
		result = append(result, agent.RecoverableWorkflowExecution{
			Key: agent.WorkflowKey{
				TenantID:       rows[i].TenantID,
				ConversationID: rows[i].ConversationID,
				ActorUserID:    rows[i].ActorUserID,
			},
			Workflow: workflow,
		})
	}
	return result, nil
}

func (s *agentWorkflowStore) MarkExecutionRecoveryRequired(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	retryAt time.Time,
) (*agent.VersionedWorkflow, error) {
	if err := s.repo.MarkExecutionRecoveryRequired(
		ctx,
		repositoryWorkflowKey(key),
		expectedVersion,
		executionToken,
		retryAt,
	); err != nil {
		return nil, mapWorkflowRepositoryError(err)
	}
	row, err := s.repo.Load(ctx, repositoryWorkflowKey(key))
	if err != nil {
		return nil, mapWorkflowRepositoryError(err)
	}
	return decodeAgentWorkflowRow(row)
}

func (s *agentWorkflowStore) Create(
	ctx context.Context,
	key agent.WorkflowKey,
	next *agent.WorkflowSnapshot,
) (*agent.VersionedWorkflow, error) {
	row, err := encodeAgentWorkflowRow(key, next, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, row); err != nil {
		return nil, mapWorkflowRepositoryError(err)
	}
	return decodeAgentWorkflowRow(row)
}

func (s *agentWorkflowStore) CompareAndSwap(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	next *agent.WorkflowSnapshot,
) (*agent.VersionedWorkflow, error) {
	update, normalized, err := encodeSnapshotUpdate(key, next, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.repo.CompareAndSwap(ctx, repositoryWorkflowKey(key), expectedVersion, update); err != nil {
		return nil, s.mapIdleMutationError(ctx, key, expectedVersion, err)
	}
	return versionedSnapshot(normalized, expectedVersion+1, nil), nil
}

func (s *agentWorkflowStore) DeleteIfVersion(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	_ string,
) error {
	err := s.repo.DeleteIfVersion(ctx, repositoryWorkflowKey(key), expectedVersion)
	return s.mapIdleMutationError(ctx, key, expectedVersion, err)
}

func (s *agentWorkflowStore) CreateReservedExecution(
	ctx context.Context,
	key agent.WorkflowKey,
	next *agent.WorkflowSnapshot,
	reservation agent.ReservedExecutionV1,
) (*agent.VersionedWorkflow, error) {
	row, err := encodeAgentWorkflowRow(key, next, s.now())
	if err != nil {
		return nil, err
	}
	repoReservation, _, err := encodeRepositoryReservation(reservation)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreateReservedExecution(ctx, row, repoReservation); err != nil {
		return nil, mapWorkflowRepositoryError(err)
	}
	return decodeAgentWorkflowRow(row)
}

func (s *agentWorkflowStore) ReserveExecution(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	next *agent.WorkflowSnapshot,
	reservation agent.ReservedExecutionV1,
) (*agent.VersionedWorkflow, error) {
	update, normalized, err := encodeSnapshotUpdate(key, next, s.now())
	if err != nil {
		return nil, err
	}
	repoReservation, normalizedReservation, err := encodeRepositoryReservation(reservation)
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReserveExecution(ctx, repositoryWorkflowKey(key), expectedVersion, update, repoReservation); err != nil {
		return nil, s.mapIdleMutationError(ctx, key, expectedVersion, err)
	}
	return versionedSnapshot(normalized, expectedVersion+1, &agent.PersistedExecutionV1{
		Status:      agent.WorkflowExecutionExecuting,
		Reservation: normalizedReservation,
	}), nil
}

func (s *agentWorkflowStore) RecordExecutionResult(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	result agent.PersistedExecutionResultV1,
) (*agent.VersionedWorkflow, error) {
	resultJSON, err := agent.MarshalPersistedExecutionResult(result)
	if err != nil {
		return nil, err
	}
	repoResult := repository.AgentWorkflowExecutionResult{
		ResultSchemaVersion: workflowCodecVersion,
		ResultJSON:          string(resultJSON),
		BusinessKey:         result.BusinessKey,
		WriteEffect:         string(result.WriteEffect),
		CompletedAt:         result.CompletedAt,
	}
	if err := s.repo.RecordExecutionResult(ctx, repositoryWorkflowKey(key), expectedVersion, executionToken, repoResult); err != nil {
		return nil, mapWorkflowRepositoryError(err)
	}
	return s.loadExpectedVersion(ctx, key, expectedVersion+1)
}

func (s *agentWorkflowStore) FinalizeExecution(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	next *agent.WorkflowSnapshot,
) (*agent.VersionedWorkflow, error) {
	update, normalized, err := encodeSnapshotUpdate(key, next, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.repo.FinalizeExecution(ctx, repositoryWorkflowKey(key), expectedVersion, executionToken, update); err != nil {
		return nil, s.mapFinalizeError(ctx, key, expectedVersion, executionToken, err)
	}
	return versionedSnapshot(normalized, expectedVersion+1, nil), nil
}

func (s *agentWorkflowStore) DeleteReservedExecution(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	_ string,
) error {
	return mapWorkflowRepositoryError(s.repo.DeleteReservedExecution(
		ctx,
		repositoryWorkflowKey(key),
		expectedVersion,
		executionToken,
	))
}

func (s *agentWorkflowStore) TakeoverExpiredExecution(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	previousToken string,
	next agent.ReservedExecutionV1,
) (*agent.VersionedWorkflow, error) {
	reservation, _, err := encodeRepositoryReservation(next)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if err := s.repo.TakeoverExpiredExecution(
		ctx,
		repositoryWorkflowKey(key),
		expectedVersion,
		previousToken,
		now,
		reservation,
	); err != nil {
		return nil, s.mapTakeoverError(ctx, key, expectedVersion, previousToken, now, err)
	}
	return s.loadExpectedVersion(ctx, key, expectedVersion+1)
}

func (s *agentWorkflowStore) loadExpectedVersion(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
) (*agent.VersionedWorkflow, error) {
	loaded, err := s.Load(ctx, key)
	if err != nil {
		return nil, err
	}
	if loaded == nil || loaded.Version != expectedVersion {
		return nil, agent.ErrWorkflowConflict
	}
	return loaded, nil
}

func (s *agentWorkflowStore) mapIdleMutationError(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	err error,
) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, repository.ErrAgentWorkflowConflict) {
		return err
	}
	row, loadErr := s.repo.Load(ctx, repositoryWorkflowKey(key))
	if loadErr == nil && row != nil && row.Version == expectedVersion &&
		row.ExecutionStatus != repository.AgentWorkflowExecutionIdle {
		return agent.ErrExecutionInProgress
	}
	return agent.ErrWorkflowConflict
}

func (s *agentWorkflowStore) mapFinalizeError(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	err error,
) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, repository.ErrAgentWorkflowConflict) {
		return err
	}
	row, loadErr := s.repo.Load(ctx, repositoryWorkflowKey(key))
	if loadErr == nil && row != nil && row.Version == expectedVersion &&
		row.ExecutionToken != nil && *row.ExecutionToken == executionToken &&
		row.ExecutionStatus == repository.AgentWorkflowExecutionExecuting {
		return agent.ErrExecutionInProgress
	}
	return agent.ErrWorkflowConflict
}

func (s *agentWorkflowStore) mapTakeoverError(
	ctx context.Context,
	key agent.WorkflowKey,
	expectedVersion uint64,
	previousToken string,
	now time.Time,
	err error,
) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, repository.ErrAgentWorkflowConflict) {
		return err
	}
	row, loadErr := s.repo.Load(ctx, repositoryWorkflowKey(key))
	if loadErr == nil && row != nil && row.Version == expectedVersion &&
		row.ExecutionToken != nil && *row.ExecutionToken == previousToken &&
		row.LeaseExpiresAt != nil && row.LeaseExpiresAt.After(now) {
		return agent.ErrExecutionInProgress
	}
	return agent.ErrWorkflowConflict
}

func (s *agentWorkflowStore) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

func encodeAgentWorkflowRow(
	key agent.WorkflowKey,
	snapshot *agent.WorkflowSnapshot,
	now time.Time,
) (*model.AgentWorkflow, error) {
	update, normalized, err := encodeSnapshotUpdate(key, snapshot, now)
	if err != nil {
		return nil, err
	}
	return &model.AgentWorkflow{
		TenantID:              key.TenantID,
		ConversationID:        key.ConversationID,
		ActorUserID:           key.ActorUserID,
		WorkflowID:            update.WorkflowID,
		WorkflowType:          update.WorkflowType,
		WorkflowState:         update.WorkflowState,
		Version:               1,
		SnapshotSchemaVersion: update.SnapshotSchemaVersion,
		SnapshotJSON:          update.SnapshotJSON,
		ExecutionStatus:       repository.AgentWorkflowExecutionIdle,
		ExpiresAt:             normalized.ExpiresAt,
		CreatedAt:             normalized.CreatedAt,
		UpdatedAt:             normalized.UpdatedAt,
	}, nil
}

func encodeSnapshotUpdate(
	key agent.WorkflowKey,
	snapshot *agent.WorkflowSnapshot,
	now time.Time,
) (repository.AgentWorkflowSnapshotUpdate, *agent.WorkflowSnapshot, error) {
	if snapshot == nil {
		return repository.AgentWorkflowSnapshotUpdate{}, nil, errors.New("workflow snapshot is required")
	}
	normalized := *snapshot
	normalized.TenantID = key.TenantID
	normalized.ConversationID = key.ConversationID
	normalized.ActorUserID = key.ActorUserID
	now = now.UTC().Truncate(time.Millisecond)
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	if normalized.ExpiresAt.IsZero() {
		normalized.ExpiresAt = now.Add(30 * time.Minute)
	}
	payload, err := agent.MarshalWorkflowSnapshot(&normalized)
	if err != nil {
		return repository.AgentWorkflowSnapshotUpdate{}, nil, err
	}
	canonical, err := agent.UnmarshalWorkflowSnapshot(payload)
	if err != nil {
		return repository.AgentWorkflowSnapshotUpdate{}, nil, err
	}
	return repository.AgentWorkflowSnapshotUpdate{
		WorkflowID:            canonical.ID,
		WorkflowType:          string(canonical.Type),
		WorkflowState:         string(canonical.State),
		SnapshotSchemaVersion: workflowCodecVersion,
		SnapshotJSON:          string(payload),
		ExpiresAt:             canonical.ExpiresAt,
	}, canonical, nil
}

func decodeAgentWorkflowRow(row *model.AgentWorkflow) (*agent.VersionedWorkflow, error) {
	if row == nil {
		return nil, nil
	}
	if row.SnapshotSchemaVersion != workflowCodecVersion {
		return nil, fmt.Errorf("unsupported workflow snapshot schema version %d", row.SnapshotSchemaVersion)
	}
	snapshot, err := agent.UnmarshalWorkflowSnapshot([]byte(row.SnapshotJSON))
	if err != nil {
		return nil, err
	}
	if snapshot.ID != row.WorkflowID || string(snapshot.Type) != row.WorkflowType ||
		string(snapshot.State) != row.WorkflowState {
		return nil, errors.New("workflow snapshot projection mismatch")
	}
	if snapshot.TenantID != row.TenantID ||
		snapshot.ConversationID != row.ConversationID ||
		snapshot.ActorUserID != row.ActorUserID {
		return nil, errors.New("workflow snapshot identity does not match database key")
	}
	snapshot.TenantID = row.TenantID
	snapshot.ConversationID = row.ConversationID
	snapshot.ActorUserID = row.ActorUserID
	snapshot.Version = row.Version
	snapshot.CreatedAt = row.CreatedAt
	snapshot.UpdatedAt = row.UpdatedAt
	snapshot.ExpiresAt = row.ExpiresAt
	execution, err := decodeExecution(row)
	if err != nil {
		return nil, err
	}
	return &agent.VersionedWorkflow{Snapshot: snapshot, Version: row.Version, Execution: execution}, nil
}

func decodeExecution(row *model.AgentWorkflow) (*agent.PersistedExecutionV1, error) { //nolint:gocyclo // Database execution authority is validated field-by-field before use.
	switch row.ExecutionStatus {
	case repository.AgentWorkflowExecutionIdle:
		if hasAnyExecutionAuthority(row) {
			return nil, errors.New("idle workflow contains execution authority")
		}
		return nil, nil
	case repository.AgentWorkflowExecutionExecuting,
		repository.AgentWorkflowExecutionResultRecorded,
		repository.AgentWorkflowExecutionRecoveryRequired:
	default:
		return nil, fmt.Errorf("unsupported execution status %q", row.ExecutionStatus)
	}
	if row.ExecutionToken == nil || row.ExecutionOperation == nil || row.BusinessKey == nil ||
		row.RequestID == nil || row.ExecutionRequestSchemaVersion == nil ||
		row.ExecutionRequestJSON == nil || row.LeaseExpiresAt == nil {
		return nil, errors.New("active workflow is missing execution authority")
	}
	if *row.ExecutionRequestSchemaVersion != workflowCodecVersion {
		return nil, errors.New("unsupported execution request schema version")
	}
	reservation, err := agent.UnmarshalReservedExecution([]byte(*row.ExecutionRequestJSON))
	if err != nil {
		return nil, err
	}
	if reservation.ExecutionToken != *row.ExecutionToken ||
		reservation.Operation != *row.ExecutionOperation ||
		reservation.BusinessKey != *row.BusinessKey ||
		reservation.AttemptRequestID != *row.RequestID ||
		!reservation.LeaseExpiresAt.Equal(row.LeaseExpiresAt.UTC().Truncate(time.Millisecond)) {
		return nil, errors.New("execution request projection mismatch")
	}
	execution := &agent.PersistedExecutionV1{
		Status:      agent.WorkflowExecutionStatus(row.ExecutionStatus),
		Reservation: reservation,
	}
	hasResult := row.ExecutionResultSchemaVersion != nil || row.ExecutionResultJSON != nil || row.WriteEffect != nil
	if row.ExecutionStatus == repository.AgentWorkflowExecutionExecuting && hasResult {
		return nil, errors.New("executing workflow contains result authority")
	}
	if row.ExecutionStatus == repository.AgentWorkflowExecutionResultRecorded && !hasResult {
		return nil, errors.New("recorded execution is missing result authority")
	}
	if hasResult {
		if row.ExecutionResultSchemaVersion == nil || row.ExecutionResultJSON == nil || row.WriteEffect == nil {
			return nil, errors.New("execution contains partial result authority")
		}
		if *row.ExecutionResultSchemaVersion != workflowCodecVersion {
			return nil, errors.New("unsupported execution result schema version")
		}
		result, err := agent.UnmarshalPersistedExecutionResult([]byte(*row.ExecutionResultJSON))
		if err != nil {
			return nil, err
		}
		if result.BusinessKey != *row.BusinessKey || string(result.WriteEffect) != *row.WriteEffect {
			return nil, errors.New("execution result projection mismatch")
		}
		execution.Result = &result
	}
	return execution, nil
}

func hasAnyExecutionAuthority(row *model.AgentWorkflow) bool {
	return row.ExecutionToken != nil ||
		row.ExecutionOperation != nil ||
		row.BusinessKey != nil ||
		row.RequestID != nil ||
		row.ExecutionRequestSchemaVersion != nil ||
		row.ExecutionRequestJSON != nil ||
		row.ExecutionResultSchemaVersion != nil ||
		row.ExecutionResultJSON != nil ||
		row.WriteEffect != nil ||
		row.LeaseExpiresAt != nil
}

func encodeRepositoryReservation(
	reservation agent.ReservedExecutionV1,
) (repository.AgentWorkflowReservation, agent.ReservedExecutionV1, error) {
	payload, err := agent.MarshalReservedExecution(reservation)
	if err != nil {
		return repository.AgentWorkflowReservation{}, agent.ReservedExecutionV1{}, err
	}
	normalized, err := agent.UnmarshalReservedExecution(payload)
	if err != nil {
		return repository.AgentWorkflowReservation{}, agent.ReservedExecutionV1{}, err
	}
	return repository.AgentWorkflowReservation{
		ExecutionToken:       normalized.ExecutionToken,
		ExecutionOperation:   normalized.Operation,
		BusinessKey:          normalized.BusinessKey,
		RequestID:            normalized.AttemptRequestID,
		RequestSchemaVersion: workflowCodecVersion,
		RequestJSON:          string(payload),
		LeaseExpiresAt:       normalized.LeaseExpiresAt,
	}, normalized, nil
}

func repositoryWorkflowKey(key agent.WorkflowKey) repository.AgentWorkflowKey {
	return repository.AgentWorkflowKey{
		TenantID:       key.TenantID,
		ConversationID: key.ConversationID,
		ActorUserID:    key.ActorUserID,
	}
}

func versionedSnapshot(
	snapshot *agent.WorkflowSnapshot,
	version uint64,
	execution *agent.PersistedExecutionV1,
) *agent.VersionedWorkflow {
	next := *snapshot
	next.Version = version
	return &agent.VersionedWorkflow{Snapshot: &next, Version: version, Execution: execution}
}

func mapWorkflowRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, repository.ErrAgentWorkflowConflict) {
		return agent.ErrWorkflowConflict
	}
	return err
}
