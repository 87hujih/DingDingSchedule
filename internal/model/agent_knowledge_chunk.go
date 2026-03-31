package model

import "time"

// AgentKnowledgeChunk 知识文档切片。
type AgentKnowledgeChunk struct {
	ID         uint   `gorm:"primaryKey"`
	TenantID   uint   `gorm:"not null;index"`
	DocumentID uint   `gorm:"not null;index;uniqueIndex:uniq_doc_chunk"`
	ChunkIndex int    `gorm:"not null;uniqueIndex:uniq_doc_chunk"`
	Heading    string `gorm:"size:200"`
	Body       string `gorm:"type:text"`
	SearchText string `gorm:"type:text"`
	SourceRef  string `gorm:"size:255"`
	DocType    string `gorm:"size:32;index"`
	Audience   string `gorm:"size:32;index"`
	Intent     string `gorm:"size:32;index"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// TableName 返回规则知识切片表名。
func (AgentKnowledgeChunk) TableName() string {
	return "agent_knowledge_chunks"
}
