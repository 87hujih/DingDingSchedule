package model

import "time"

// AgentWorkflow persists the latest workflow snapshot for one tenant conversation actor key.
type AgentWorkflow struct {
	ID             uint64    `gorm:"primaryKey"`
	TenantID       uint      `gorm:"not null;uniqueIndex:uk_agent_workflow_key"`
	ConversationID string    `gorm:"size:128;not null;uniqueIndex:uk_agent_workflow_key"`
	ActorUserID    uint      `gorm:"not null;uniqueIndex:uk_agent_workflow_key"`
	WorkflowID     string    `gorm:"size:64;not null"`
	WorkflowType   string    `gorm:"size:64;not null"`
	State          string    `gorm:"size:32;not null"`
	Version        uint64    `gorm:"not null;default:1"`
	SnapshotJSON   string    `gorm:"type:longtext;not null"`
	ExpiresAt      time.Time `gorm:"index;not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
