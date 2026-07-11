package app

import (
	"context"
	"encoding/json"
	"time"

	"schedule_server/internal/agent"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
)

type agentWorkflowStore struct {
	repo  repository.AgentWorkflowRepository
	clock func() time.Time
}

func newAgentWorkflowStore(repo repository.AgentWorkflowRepository, clock func() time.Time) agent.WorkflowStore {
	if repo == nil {
		return nil
	}
	if clock == nil {
		clock = time.Now
	}
	return &agentWorkflowStore{repo: repo, clock: clock}
}

func (s *agentWorkflowStore) Load(ctx context.Context, key agent.WorkflowKey) (*agent.VersionedWorkflow, error) {
	row, err := s.repo.Load(ctx, key, s.clock())
	if err != nil || row == nil {
		return nil, err
	}
	return decodeAgentWorkflow(row)
}

func (s *agentWorkflowStore) Create(ctx context.Context, key agent.WorkflowKey, next *agent.WorkflowSnapshot) (*agent.VersionedWorkflow, error) {
	if next == nil {
		return nil, nil
	}
	now := s.clock()
	prepared := prepareAgentWorkflowSnapshot(key, next, 1, now, time.Time{})
	row, err := encodeAgentWorkflow(prepared, 1)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, row, now); err != nil {
		return nil, err
	}
	return decodeAgentWorkflow(row)
}

func (s *agentWorkflowStore) CompareAndSwap(ctx context.Context, key agent.WorkflowKey, expected uint64, next *agent.WorkflowSnapshot) (*agent.VersionedWorkflow, error) {
	if next == nil {
		return nil, nil
	}
	now := s.clock()
	createdAt := next.CreatedAt
	if createdAt.IsZero() {
		current, err := s.repo.Load(ctx, key, now)
		if err != nil {
			return nil, err
		}
		if current != nil {
			decoded, err := decodeAgentWorkflow(current)
			if err != nil {
				return nil, err
			}
			createdAt = decoded.Snapshot.CreatedAt
		}
	}
	prepared := prepareAgentWorkflowSnapshot(key, next, expected+1, now, createdAt)
	row, err := encodeAgentWorkflow(prepared, expected+1)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CompareAndSwap(ctx, key, expected, row, now); err != nil {
		return nil, err
	}
	return decodeAgentWorkflow(row)
}

func (s *agentWorkflowStore) DeleteIfVersion(ctx context.Context, key agent.WorkflowKey, expected uint64, _ string) error {
	return s.repo.DeleteIfVersion(ctx, key, expected)
}

func (s *agentWorkflowStore) ReserveExecution(ctx context.Context, key agent.WorkflowKey, expected uint64, base *agent.WorkflowSnapshot, lease agent.WorkflowExecutionLease) (*agent.VersionedWorkflow, error) {
	if base == nil {
		return nil, agent.ErrWorkflowConflict
	}
	now := s.clock()
	lease.StartedAt = now
	lease.LeaseExpiresAt = now.Add(agent.WorkflowExecutionLeaseDuration)
	if expected > 0 {
		current, err := s.Load(ctx, key)
		if err != nil {
			return nil, err
		}
		if current == nil || current.Version != expected {
			return nil, agent.ErrWorkflowConflict
		}
		if current.Snapshot.State == agent.WorkflowExecuting && current.Snapshot.ExecutionLease != nil &&
			current.Snapshot.ExecutionLease.LeaseExpiresAt.After(now) {
			return nil, agent.ErrWorkflowConflict
		}
	}
	next := *base
	next.State = agent.WorkflowExecuting
	next.ExecutionLease = &lease
	if next.ExpiresAt.Before(lease.LeaseExpiresAt) {
		next.ExpiresAt = lease.LeaseExpiresAt
	}
	if expected == 0 {
		return s.Create(ctx, key, &next)
	}
	return s.CompareAndSwap(ctx, key, expected, &next)
}

func (s *agentWorkflowStore) FinalizeExecution(ctx context.Context, key agent.WorkflowKey, expected uint64, executionToken string, next *agent.WorkflowSnapshot) error {
	var row *model.AgentWorkflow
	if next != nil {
		prepared := prepareAgentWorkflowSnapshot(key, next, expected+1, s.clock(), next.CreatedAt)
		prepared.ExecutionLease = nil
		var err error
		row, err = encodeAgentWorkflow(prepared, expected+1)
		if err != nil {
			return err
		}
	}
	return s.repo.FinalizeExecution(ctx, key, expected, executionToken, row)
}

func prepareAgentWorkflowSnapshot(key agent.WorkflowKey, source *agent.WorkflowSnapshot, version uint64, now, createdAt time.Time) *agent.WorkflowSnapshot {
	next := *source
	next.TenantID = key.TenantID
	next.ConversationID = key.ConversationID
	next.ActorUserID = key.ActorUserID
	next.Version = int64(version)
	if createdAt.IsZero() {
		createdAt = source.CreatedAt
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	next.CreatedAt = createdAt
	next.UpdatedAt = now
	if next.ExpiresAt.IsZero() {
		next.ExpiresAt = now.Add(30 * time.Minute)
	}
	return &next
}

func encodeAgentWorkflow(snapshot *agent.WorkflowSnapshot, version uint64) (*model.AgentWorkflow, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return &model.AgentWorkflow{
		TenantID:       snapshot.TenantID,
		ConversationID: snapshot.ConversationID,
		ActorUserID:    snapshot.ActorUserID,
		WorkflowID:     snapshot.ID,
		WorkflowType:   string(snapshot.Type),
		State:          string(snapshot.State),
		Version:        version,
		SnapshotJSON:   string(payload),
		ExpiresAt:      snapshot.ExpiresAt,
	}, nil
}

func decodeAgentWorkflow(row *model.AgentWorkflow) (*agent.VersionedWorkflow, error) {
	var snapshot agent.WorkflowSnapshot
	if err := json.Unmarshal([]byte(row.SnapshotJSON), &snapshot); err != nil {
		return nil, err
	}
	snapshot.TenantID = row.TenantID
	snapshot.ConversationID = row.ConversationID
	snapshot.ActorUserID = row.ActorUserID
	snapshot.Version = int64(row.Version)
	snapshot.ExpiresAt = row.ExpiresAt
	return &agent.VersionedWorkflow{Snapshot: &snapshot, Version: row.Version}, nil
}
