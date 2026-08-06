package model

import "time"

// AgentWorkflow persists the business workflow snapshot and the independent
// execution authority used for fencing/recovery.
type AgentWorkflow struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement"`
	TenantID       uint   `gorm:"not null;uniqueIndex:uk_agent_workflow_key,priority:1"`
	ConversationID string `gorm:"size:128;not null;uniqueIndex:uk_agent_workflow_key,priority:2"`
	ActorUserID    uint   `gorm:"not null;uniqueIndex:uk_agent_workflow_key,priority:3"`

	WorkflowID    string `gorm:"size:64;not null"`
	WorkflowType  string `gorm:"size:64;not null"`
	WorkflowState string `gorm:"size:32;not null"`
	Version       uint64 `gorm:"not null;default:1"`

	SnapshotSchemaVersion uint16 `gorm:"not null;default:1"`
	SnapshotJSON          string `gorm:"type:longtext;not null"`

	ExecutionStatus               string  `gorm:"size:32;not null;default:idle;index:idx_agent_workflow_recovery,priority:1"`
	ExecutionToken                *string `gorm:"size:64"`
	ExecutionOperation            *string `gorm:"size:64"`
	BusinessKey                   *string `gorm:"type:char(64)"`
	RequestID                     *string `gorm:"size:64"`
	ExecutionRequestSchemaVersion *uint16
	ExecutionRequestJSON          *string `gorm:"type:longtext"`
	ExecutionResultSchemaVersion  *uint16
	ExecutionResultJSON           *string `gorm:"type:longtext"`
	WriteEffect                   *string `gorm:"size:32"`

	LeaseExpiresAt *time.Time `gorm:"precision:3;index:idx_agent_workflow_recovery,priority:2"`
	ExpiresAt      time.Time  `gorm:"precision:3;not null;index:idx_agent_workflow_expiry"`
	CreatedAt      time.Time  `gorm:"precision:3;not null"`
	UpdatedAt      time.Time  `gorm:"precision:3;not null"`
}

func (AgentWorkflow) TableName() string {
	return "agent_workflows"
}
