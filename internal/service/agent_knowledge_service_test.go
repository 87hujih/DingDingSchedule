package service

import (
	"context"
	"slices"
	"strings"
	"testing"

	"schedule_server/internal/model"
	"schedule_server/internal/repository"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestAgentKnowledgeServiceSearchReturnsTenantScopedRankedChunks 验证检索结果会按租户隔离并按分数排序。
func TestAgentKnowledgeServiceSearchReturnsTenantScopedRankedChunks(t *testing.T) {
	db := newAgentKnowledgeTestDB(t)

	repo := repository.NewAgentKnowledgeRepository(db)
	svc := NewAgentKnowledgeService(repo, zap.NewNop().Sugar())

	const tenantID = uint(1)
	const otherTenantID = uint(2)

	mustCreateKnowledgeDocument(t, db, &model.AgentKnowledgeDocument{
		TenantID:   tenantID,
		Title:      "考勤规则",
		SourcePath: "docs/attendance-rules.md",
		SourceType: "markdown",
		Status:     "active",
	}, []model.AgentKnowledgeChunk{
		{
			TenantID:   tenantID,
			ChunkIndex: 1,
			Heading:    "迟到判定",
			Body:       "迟到规则说明：超过宽限时间后签到记为迟到。",
			SearchText: "迟到 规则 说明 超过 宽限 时间 后 签到 记为 迟到",
			SourceRef:  "考勤规则#1",
		},
		{
			TenantID:   tenantID,
			ChunkIndex: 2,
			Heading:    "作息说明",
			Body:       "作息时间配置会影响节次开始与结束时间。",
			SearchText: "作息 时间 配置 影响 节次 开始 结束 时间",
			SourceRef:  "考勤规则#2",
		},
	})

	mustCreateKnowledgeDocument(t, db, &model.AgentKnowledgeDocument{
		TenantID:   otherTenantID,
		Title:      "其他租户考勤规则",
		SourcePath: "docs/other-attendance-rules.md",
		SourceType: "markdown",
		Status:     "active",
	}, []model.AgentKnowledgeChunk{
		{
			TenantID:   otherTenantID,
			ChunkIndex: 1,
			Heading:    "迟到判定",
			Body:       "其他租户的迟到规则，不应被当前租户检索到。",
			SearchText: "其他 租户 迟到 规则 不应 被 当前 租户 检索",
			SourceRef:  "其他租户考勤规则#1",
		},
	})

	hits, err := svc.Search(context.Background(), tenantID, "迟到规则", 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("len(hits) = %d, want 2", len(hits))
	}

	if hits[0].TenantID != tenantID || hits[1].TenantID != tenantID {
		t.Fatalf("Search() returned cross-tenant hits: %+v", hits)
	}
	if hits[0].Title != "考勤规则" {
		t.Fatalf("top hit title = %q, want 考勤规则", hits[0].Title)
	}
	if hits[0].SourcePath != "docs/attendance-rules.md" {
		t.Fatalf("top hit source path = %q, want docs/attendance-rules.md", hits[0].SourcePath)
	}
	if hits[0].SourceRef != "考勤规则#1" {
		t.Fatalf("top hit source ref = %q, want 考勤规则#1", hits[0].SourceRef)
	}
	if hits[0].Score < hits[1].Score {
		t.Fatalf("expected top hit score >= second hit score, got %d < %d", hits[0].Score, hits[1].Score)
	}
}

// TestAgentKnowledgeServiceSearchRanksChineseRuleQuestions 验证中文整句规则问句会命中对应知识片段，而不是退化为按文档顺序返回。
func TestAgentKnowledgeServiceSearchRanksChineseRuleQuestions(t *testing.T) {
	db := newAgentKnowledgeTestDB(t)

	repo := repository.NewAgentKnowledgeRepository(db)
	svc := NewAgentKnowledgeService(repo, zap.NewNop().Sugar())

	const tenantID = uint(1)

	mustCreateKnowledgeDocument(t, db, &model.AgentKnowledgeDocument{
		TenantID:   tenantID,
		Title:      "后台操作知识说明",
		SourcePath: "docs/agent-knowledge/admin-operations-guide.md",
		SourceType: "markdown",
		Status:     "active",
	}, []model.AgentKnowledgeChunk{
		{
			TenantID:   tenantID,
			ChunkIndex: 1,
			Heading:    "后台怎么启用",
			Body:       "后台依赖 goadmin.enable 开关和系统表初始化。",
			SearchText: normalizeSearchText("后台怎么启用 后台依赖 goadmin.enable 开关和系统表初始化。"),
			SourceRef:  "后台操作知识说明#1",
		},
	})

	mustCreateKnowledgeDocument(t, db, &model.AgentKnowledgeDocument{
		TenantID:   tenantID,
		Title:      "考勤规则说明",
		SourcePath: "docs/agent-knowledge/attendance-rules.md",
		SourceType: "markdown",
		Status:     "active",
	}, []model.AgentKnowledgeChunk{
		{
			TenantID:   tenantID,
			ChunkIndex: 1,
			Heading:    "迟到判定",
			Body:       "上课开始后 10 分钟内打卡记为正常，超过 10 分钟打卡视为迟到。",
			SearchText: normalizeSearchText("迟到判定 上课开始后 10 分钟内打卡记为正常，超过 10 分钟打卡视为迟到。"),
			SourceRef:  "考勤规则说明#1",
		},
	})

	hits, err := svc.Search(context.Background(), tenantID, "考勤迟到怎么判定？", 4)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("Search() returned no hits")
	}
	if hits[0].SourceRef != "考勤规则说明#1" {
		t.Fatalf("top hit source ref = %q, want %q", hits[0].SourceRef, "考勤规则说明#1")
	}
	if hits[0].Score <= hits[len(hits)-1].Score {
		t.Fatalf("expected top hit score to exceed generic doc score, got top=%d last=%d", hits[0].Score, hits[len(hits)-1].Score)
	}
}

// TestAgentKnowledgeServiceBuildChunksPreservesSourceRefs 验证切片时会稳定生成来源引用。
func TestAgentKnowledgeServiceBuildChunksPreservesSourceRefs(t *testing.T) {
	svc := NewAgentKnowledgeService(nil, zap.NewNop().Sugar())

	chunks := svc.BuildChunks("考勤规则", "docs/attendance-rules.md", strings.TrimSpace(`
# 迟到判定

超过宽限时间后签到记为迟到。

## 休息日

休息日用户不会进入应到名单。
`))

	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(chunks))
	}

	sourceRefs := []string{chunks[0].SourceRef, chunks[1].SourceRef}
	if !slices.Equal(sourceRefs, []string{"考勤规则#1", "考勤规则#2"}) {
		t.Fatalf("source refs = %v, want [考勤规则#1 考勤规则#2]", sourceRefs)
	}
	if chunks[0].Heading != "迟到判定" {
		t.Fatalf("first chunk heading = %q, want 迟到判定", chunks[0].Heading)
	}
	if chunks[1].Heading != "休息日" {
		t.Fatalf("second chunk heading = %q, want 休息日", chunks[1].Heading)
	}
}

// TestAgentKnowledgeServiceSyncMarkdownReplacesDocumentChunks 验证重复同步同一路径文档时会重建切片。
func TestAgentKnowledgeServiceSyncMarkdownReplacesDocumentChunks(t *testing.T) {
	db := newAgentKnowledgeTestDB(t)

	repo := repository.NewAgentKnowledgeRepository(db)
	svc := NewAgentKnowledgeService(repo, zap.NewNop().Sugar())

	const tenantID = uint(1)

	firstSync, err := svc.SyncMarkdownDocuments(context.Background(), tenantID, []MarkdownKnowledgeDocument{
		{
			Title:      "考勤规则",
			SourcePath: "docs/attendance-rules.md",
			Content: strings.TrimSpace(`
# 迟到判定

超过宽限时间后签到记为迟到。
`),
		},
	})
	if err != nil {
		t.Fatalf("first SyncMarkdownDocuments() error = %v", err)
	}
	if firstSync.DocumentsSynced != 1 {
		t.Fatalf("first sync document count = %d, want 1", firstSync.DocumentsSynced)
	}
	if firstSync.ChunksCreated != 1 {
		t.Fatalf("first sync chunk count = %d, want 1", firstSync.ChunksCreated)
	}

	secondSync, err := svc.SyncMarkdownDocuments(context.Background(), tenantID, []MarkdownKnowledgeDocument{
		{
			Title:      "考勤规则",
			SourcePath: "docs/attendance-rules.md",
			Content: strings.TrimSpace(`
# 迟到判定

超过宽限时间后签到记为迟到。

## 休息日

休息日用户不会进入应到名单。
`),
		},
	})
	if err != nil {
		t.Fatalf("second SyncMarkdownDocuments() error = %v", err)
	}
	if secondSync.DocumentsSynced != 1 {
		t.Fatalf("second sync document count = %d, want 1", secondSync.DocumentsSynced)
	}
	if secondSync.ChunksCreated != 2 {
		t.Fatalf("second sync chunk count = %d, want 2", secondSync.ChunksCreated)
	}

	var docCount int64
	if err := db.Model(&model.AgentKnowledgeDocument{}).Where("tenant_id = ?", tenantID).Count(&docCount).Error; err != nil {
		t.Fatalf("count knowledge documents: %v", err)
	}
	if docCount != 1 {
		t.Fatalf("document count = %d, want 1", docCount)
	}

	var doc model.AgentKnowledgeDocument
	if err := db.Where("tenant_id = ? AND source_path = ?", tenantID, "docs/attendance-rules.md").First(&doc).Error; err != nil {
		t.Fatalf("find synced document: %v", err)
	}
	if doc.ContentHash == "" {
		t.Fatalf("expected content hash to be populated")
	}

	var chunks []model.AgentKnowledgeChunk
	if err := db.Where("tenant_id = ? AND document_id = ?", tenantID, doc.ID).Order("chunk_index ASC").Find(&chunks).Error; err != nil {
		t.Fatalf("list synced chunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d, want 2", len(chunks))
	}
	if chunks[0].SourceRef != "考勤规则#1" || chunks[1].SourceRef != "考勤规则#2" {
		t.Fatalf("unexpected chunk source refs: %+v", chunks)
	}
	if chunks[0].Heading != "迟到判定" || chunks[1].Heading != "休息日" {
		t.Fatalf("unexpected chunk headings: %+v", chunks)
	}
}

// newAgentKnowledgeTestDB 创建知识服务测试使用的内存 SQLite 数据库。
func newAgentKnowledgeTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := "file:agent-knowledge-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.AgentKnowledgeDocument{}, &model.AgentKnowledgeChunk{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// mustCreateKnowledgeDocument 为测试预置一条文档和其切片数据。
func mustCreateKnowledgeDocument(t *testing.T, db *gorm.DB, doc *model.AgentKnowledgeDocument, chunks []model.AgentKnowledgeChunk) {
	t.Helper()

	if err := db.Create(doc).Error; err != nil {
		t.Fatalf("create document: %v", err)
	}
	for i := range chunks {
		chunks[i].DocumentID = doc.ID
	}
	if err := db.Create(&chunks).Error; err != nil {
		t.Fatalf("create chunks: %v", err)
	}
}
