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
	if err := s.repo.CompareAndSwap(ctx, key, expected, row); err != nil {
		return nil, err
	}
	return decodeAgentWorkflow(row)
}

func (s *agentWorkflowStore) DeleteIfVersion(ctx context.Context, key agent.WorkflowKey, expected uint64, _ string) error {
	return s.repo.DeleteIfVersion(ctx, key, expected)
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
