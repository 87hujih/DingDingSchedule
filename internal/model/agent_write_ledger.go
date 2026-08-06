package model

import "time"

// AgentWriteLedger records the converged effect of a stable business key in
// the same database transaction as the subscription mutation.
type AgentWriteLedger struct {
	ID          uint      `gorm:"primaryKey"`
	TenantID    uint      `gorm:"not null;uniqueIndex:uniq_agent_write_business_key"`
	BusinessKey string    `gorm:"not null;size:64;uniqueIndex:uniq_agent_write_business_key"`
	Operation   string    `gorm:"not null;size:64"`
	WriteEffect string    `gorm:"not null;size:32"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (AgentWriteLedger) TableName() string {
	return "agent_write_ledgers"
}
