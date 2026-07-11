package repository

import (
	"context"
	"errors"
	"time"

	"schedule_server/internal/agent"
	"schedule_server/internal/model"
	"schedule_server/internal/tenantctx"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentWorkflowRepository persists workflow snapshots keyed by their explicit tenant identity.
type AgentWorkflowRepository interface {
	Load(ctx context.Context, key agent.WorkflowKey, now time.Time) (*model.AgentWorkflow, error)
	Create(ctx context.Context, workflow *model.AgentWorkflow, now time.Time) error
	CompareAndSwap(ctx context.Context, key agent.WorkflowKey, expected uint64, next *model.AgentWorkflow, now time.Time) error
	DeleteIfVersion(ctx context.Context, key agent.WorkflowKey, expected uint64) error
}

type agentWorkflowRepository struct {
	db *gorm.DB
}

func NewAgentWorkflowRepository(db *gorm.DB) AgentWorkflowRepository {
	return &agentWorkflowRepository{db: db}
}

func (r *agentWorkflowRepository) Load(ctx context.Context, key agent.WorkflowKey, now time.Time) (*model.AgentWorkflow, error) {
	var workflow model.AgentWorkflow
	err := r.scoped(ctx).
		Where("tenant_id = ? AND conversation_id = ? AND actor_user_id = ? AND expires_at > ?",
			key.TenantID, key.ConversationID, key.ActorUserID, now).
		First(&workflow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &workflow, err
}

func (r *agentWorkflowRepository) Create(ctx context.Context, workflow *model.AgentWorkflow, now time.Time) error {
	return r.scoped(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.AgentWorkflow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND conversation_id = ? AND actor_user_id = ?",
				workflow.TenantID, workflow.ConversationID, workflow.ActorUserID).
			First(&current).Error
		switch {
		case err == nil && current.ExpiresAt.After(now):
			return agent.ErrWorkflowConflict
		case err == nil:
			if err := tx.Delete(&current).Error; err != nil {
				return err
			}
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}
		if err := tx.Create(workflow).Error; err != nil {
			return agentWorkflowConflictError(err)
		}
		return nil
	})
}

func (r *agentWorkflowRepository) CompareAndSwap(ctx context.Context, key agent.WorkflowKey, expected uint64, next *model.AgentWorkflow, now time.Time) error {
	result := r.scoped(ctx).Model(&model.AgentWorkflow{}).
		Where("tenant_id = ? AND conversation_id = ? AND actor_user_id = ? AND version = ? AND expires_at > ?",
			key.TenantID, key.ConversationID, key.ActorUserID, expected, now).
		Updates(map[string]any{
			"workflow_id":   next.WorkflowID,
			"workflow_type": next.WorkflowType,
			"version":       expected + 1,
			"snapshot_json": next.SnapshotJSON,
			"state":         next.State,
			"expires_at":    next.ExpiresAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agent.ErrWorkflowConflict
	}
	return nil
}

func (r *agentWorkflowRepository) DeleteIfVersion(ctx context.Context, key agent.WorkflowKey, expected uint64) error {
	result := r.scoped(ctx).
		Where("tenant_id = ? AND conversation_id = ? AND actor_user_id = ? AND version = ?",
			key.TenantID, key.ConversationID, key.ActorUserID, expected).
		Delete(&model.AgentWorkflow{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := r.scoped(ctx).Model(&model.AgentWorkflow{}).
			Where("tenant_id = ? AND conversation_id = ? AND actor_user_id = ?",
				key.TenantID, key.ConversationID, key.ActorUserID).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return agent.ErrWorkflowConflict
		}
	}
	return nil
}

func (r *agentWorkflowRepository) scoped(ctx context.Context) *gorm.DB {
	return r.db.WithContext(tenantctx.WithSkipTenantScope(ctx))
}

func agentWorkflowConflictError(err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return agent.ErrWorkflowConflict
	}
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return agent.ErrWorkflowConflict
	}
	return err
}
