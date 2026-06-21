package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentKnowledgeSearchRow 检索阶段使用的文档+切片联合视图。
type AgentKnowledgeSearchRow struct {
	TenantID   uint
	DocumentID uint
	Title      string
	SourcePath string
	DocType    string
	Audience   string
	Intent     string
	ChunkIndex int
	Heading    string
	Body       string
	SearchText string
	SourceRef  string
}

// AgentKnowledgeRepository 知识文档与切片仓储。
type AgentKnowledgeRepository interface {
	UpsertDocument(ctx context.Context, doc *model.AgentKnowledgeDocument) error
	ReplaceChunks(ctx context.Context, tenantID uint, documentID uint, chunks []model.AgentKnowledgeChunk) error
	ListChunksByTenant(ctx context.Context, tenantID uint) ([]AgentKnowledgeSearchRow, error)
	SearchChunks(ctx context.Context, tenantID uint, query string) ([]AgentKnowledgeSearchRow, error)
}

type agentKnowledgeRepository struct {
	db *gorm.DB
}

// NewAgentKnowledgeRepository 创建知识文档仓储实现。
func NewAgentKnowledgeRepository(db *gorm.DB) AgentKnowledgeRepository {
	return &agentKnowledgeRepository{db: db}
}

// UpsertDocument 按租户和来源路径创建或更新知识文档元信息。
func (r *agentKnowledgeRepository) UpsertDocument(ctx context.Context, doc *model.AgentKnowledgeDocument) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.AgentKnowledgeDocument
		err := tx.
			Where("tenant_id = ? AND source_path = ?", doc.TenantID, doc.SourcePath).
			Limit(1).
			Find(&existing).Error
		if err == nil && existing.ID != 0 {
			existing.Title = doc.Title
			existing.SourceType = doc.SourceType
			existing.DocType = doc.DocType
			existing.Audience = doc.Audience
			existing.Intent = doc.Intent
			existing.ContentHash = doc.ContentHash
			existing.Status = doc.Status
			if updateErr := tx.Save(&existing).Error; updateErr != nil {
				return updateErr
			}
			doc.ID = existing.ID
			return nil
		}
		if err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "source_path"}},
			DoUpdates: clause.AssignmentColumns([]string{"title", "source_type", "doc_type", "audience", "intent", "content_hash", "status", "updated_at"}),
		}).Create(doc).Error
	})
}

// ReplaceChunks 用新切片全量替换指定文档的旧切片。
func (r *agentKnowledgeRepository) ReplaceChunks(ctx context.Context, tenantID uint, documentID uint, chunks []model.AgentKnowledgeChunk) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND document_id = ?", tenantID, documentID).Delete(&model.AgentKnowledgeChunk{}).Error; err != nil {
			return err
		}
		if len(chunks) == 0 {
			return nil
		}
		return tx.Create(&chunks).Error
	})
}

// ListChunksByTenant 返回指定租户下的全部文档切片。
func (r *agentKnowledgeRepository) ListChunksByTenant(ctx context.Context, tenantID uint) ([]AgentKnowledgeSearchRow, error) {
	var rows []AgentKnowledgeSearchRow
	err := r.db.WithContext(ctx).
		Table("agent_knowledge_chunks AS c").
		Select("c.tenant_id, c.document_id, d.title, d.source_path, COALESCE(NULLIF(c.doc_type, ''), d.doc_type) AS doc_type, COALESCE(NULLIF(c.audience, ''), d.audience) AS audience, COALESCE(NULLIF(c.intent, ''), d.intent) AS intent, c.chunk_index, c.heading, c.body, c.search_text, c.source_ref").
		Joins("JOIN agent_knowledge_documents AS d ON d.id = c.document_id").
		Where("c.tenant_id = ?", tenantID).
		Order("c.document_id ASC, c.chunk_index ASC").
		Scan(&rows).Error
	return rows, err
}

// SearchChunks 返回检索阶段使用的租户级切片候选集。
func (r *agentKnowledgeRepository) SearchChunks(ctx context.Context, tenantID uint, query string) ([]AgentKnowledgeSearchRow, error) {
	// 首版检索排序在 service 层完成，这里先提供租户隔离后的联合数据集。
	return r.ListChunksByTenant(ctx, tenantID)
}
