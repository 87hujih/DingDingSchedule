package repository

import (
	"context"
	"errors"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

type AgentOperationExecutionRepository interface {
	FindSucceeded(ctx context.Context, tenantID uint, businessKey string) (*model.AgentOperationExecution, error)
}

type agentOperationExecutionRepository struct{ db *gorm.DB }

func NewAgentOperationExecutionRepository(db *gorm.DB) AgentOperationExecutionRepository {
	return &agentOperationExecutionRepository{db: db}
}

func (r *agentOperationExecutionRepository) FindSucceeded(ctx context.Context, tenantID uint, businessKey string) (*model.AgentOperationExecution, error) {
	var execution model.AgentOperationExecution
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND business_key = ? AND status = ?", tenantID, businessKey, model.AgentOperationStatusSucceeded).First(&execution).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &execution, err
}
