package model

import "time"

// AgentKnowledgeDocument 规则知识文档元信息。
type AgentKnowledgeDocument struct {
	ID          uint   `gorm:"primaryKey"`
	TenantID    uint   `gorm:"not null;index;uniqueIndex:uniq_tenant_source_path"`
	Title       string `gorm:"not null;size:200"`
	SourcePath  string `gorm:"not null;size:255;uniqueIndex:uniq_tenant_source_path"`
	SourceType  string `gorm:"not null;size:50"`
	DocType     string `gorm:"size:32;index"`
	Audience    string `gorm:"size:32;index"`
	Intent      string `gorm:"size:32;index"`
	ContentHash string `gorm:"size:64"`
	Status      string `gorm:"not null;size:32;index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TableName 返回规则知识文档表名。
func (AgentKnowledgeDocument) TableName() string {
	return "agent_knowledge_documents"
}
