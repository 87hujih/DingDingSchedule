package model

import "time"

const (
	AgentOperationStatusExecuting = "executing"
	AgentOperationStatusSucceeded = "succeeded"

	AgentWriteEffectCreated   = "created"
	AgentWriteEffectUpdated   = "updated"
	AgentWriteEffectNoOp      = "no_op"
	AgentWriteEffectCancelled = "cancelled"
)

// AgentOperationExecution is the durable business-idempotency ledger for agent writes.
type AgentOperationExecution struct {
	ID             uint      `gorm:"primaryKey"`
	TenantID       uint      `gorm:"not null;uniqueIndex:uniq_agent_operation_business,priority:1"`
	BusinessKey    string    `gorm:"not null;size:191;uniqueIndex:uniq_agent_operation_business,priority:2"`
	ConversationID string    `gorm:"not null;size:191;index:idx_agent_operation_conversation"`
	Operation      string    `gorm:"not null;size:64;index"`
	Status         string    `gorm:"not null;size:32;index"`
	WriteEffect    string    `gorm:"not null;size:32"`
	ResultJSON     string    `gorm:"not null;type:text"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (*AgentOperationExecution) TableName() string { return "agent_operation_executions" }
