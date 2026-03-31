package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/tenantctx"

	"go.uber.org/zap"
)

const (
	defaultKnowledgeDocType     = "unknown"
	defaultKnowledgeAudience    = "shared"
	defaultKnowledgeIntent      = "unknown"
	knowledgeRerankWindowFactor = 4
)

var retrievalAliases = map[string][]string{
	"没同步到位":   {"同步失败", "失败处理"},
	"没能同步到位":  {"同步失败", "失败处理"},
	"会出现什么情况": {"影响", "处理"},
	"考勤逻辑":    {"考勤规则"},
}

var knowledgeIntentHints = map[string][]string{
	"attendance": {"考勤", "打卡", "迟到", "未到"},
	"leave":      {"请假", "审批", "同步", "快照"},
	"schedule":   {"课表", "节次", "作息", "排课"},
	"system":     {"系统", "功能", "能力", "介绍"},
	"adminui":    {"后台", "管理", "goadmin", "启用", "登录"},
}

var knowledgeAdminHints = []string{"后台", "管理", "goadmin", "启用", "登录", "初始化"}

// KnowledgeChunkDraft 表示尚未持久化的知识切片。
type KnowledgeChunkDraft struct {
	ChunkIndex int
	Heading    string
	Body       string
	SearchText string
	SourceRef  string
}

// KnowledgeDocumentMetadata 表示知识文档的轻量分类元数据。
type KnowledgeDocumentMetadata struct {
	DocType  string
	Audience string
	Intent   string
}

// KnowledgeHit 表示一次知识检索命中的切片。
type KnowledgeHit struct {
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
	SourceRef  string
	Score      int
}

// MarkdownKnowledgeDocument 表示待同步的 Markdown 文档。
type MarkdownKnowledgeDocument struct {
	Title      string
	SourcePath string
	Content    string
	Metadata   KnowledgeDocumentMetadata
}

// KnowledgeSyncResult 返回一次知识同步的摘要。
type KnowledgeSyncResult struct {
	DocumentsSynced int
	ChunksCreated   int
	Skipped         int
}

// AgentKnowledgeService 提供规则知识的切片与检索能力。
type AgentKnowledgeService struct {
	repo   repository.AgentKnowledgeRepository
	logger *zap.SugaredLogger
}

type knowledgeQueryPlan struct {
	CompactQueries []string
	QueryTerms     []string
}

type knowledgeCandidate struct {
	row          repository.AgentKnowledgeSearchRow
	lexicalScore int
	finalScore   int
}

// NewAgentKnowledgeService 创建规则知识服务。
func NewAgentKnowledgeService(repo repository.AgentKnowledgeRepository, logger *zap.SugaredLogger) *AgentKnowledgeService {
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &AgentKnowledgeService{
		repo:   repo,
		logger: logger,
	}
}

// NormalizeKnowledgeMetadata 统一补齐知识文档元数据缺省值。
func NormalizeKnowledgeMetadata(meta KnowledgeDocumentMetadata) KnowledgeDocumentMetadata {
	normalized := KnowledgeDocumentMetadata{
		DocType:  strings.ToLower(strings.TrimSpace(meta.DocType)),
		Audience: strings.ToLower(strings.TrimSpace(meta.Audience)),
		Intent:   strings.ToLower(strings.TrimSpace(meta.Intent)),
	}
	if normalized.DocType == "" {
		normalized.DocType = defaultKnowledgeDocType
	}
	if normalized.Audience == "" {
		normalized.Audience = defaultKnowledgeAudience
	}
	if normalized.Intent == "" {
		normalized.Intent = defaultKnowledgeIntent
	}
	return normalized
}

// Search 按租户检索与问题最相关的知识切片。
func (s *AgentKnowledgeService) Search(ctx context.Context, tenantID uint, query string, topK int) ([]KnowledgeHit, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("agent knowledge repository is nil")
	}
	if tenantID == 0 {
		return []KnowledgeHit{}, nil
	}
	rows, err := s.repo.SearchChunks(ctx, tenantID, query)
	if err != nil {
		return nil, err
	}

	queryPlan := buildKnowledgeQueryPlan(query)
	if len(queryPlan.CompactQueries) == 0 {
		return []KnowledgeHit{}, nil
	}

	candidates := make([]knowledgeCandidate, 0, len(rows))
	for _, row := range rows {
		score := scoreKnowledgeLexical(row, queryPlan.CompactQueries, queryPlan.QueryTerms)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, knowledgeCandidate{
			row:          row,
			lexicalScore: score,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].lexicalScore == candidates[j].lexicalScore {
			if candidates[i].row.DocumentID == candidates[j].row.DocumentID {
				return candidates[i].row.ChunkIndex < candidates[j].row.ChunkIndex
			}
			return candidates[i].row.DocumentID < candidates[j].row.DocumentID
		}
		return candidates[i].lexicalScore > candidates[j].lexicalScore
	})

	rerankLimit := len(candidates)
	if topK > 0 {
		window := topK * knowledgeRerankWindowFactor
		if window < topK {
			window = topK
		}
		if rerankLimit > window {
			rerankLimit = window
		}
	}
	candidates = candidates[:rerankLimit]

	for i := range candidates {
		candidates[i].finalScore = candidates[i].lexicalScore + scoreKnowledgeMetadata(candidates[i].row, queryPlan.QueryTerms)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].finalScore == candidates[j].finalScore {
			if candidates[i].lexicalScore == candidates[j].lexicalScore {
				if candidates[i].row.DocumentID == candidates[j].row.DocumentID {
					return candidates[i].row.ChunkIndex < candidates[j].row.ChunkIndex
				}
				return candidates[i].row.DocumentID < candidates[j].row.DocumentID
			}
			return candidates[i].lexicalScore > candidates[j].lexicalScore
		}
		return candidates[i].finalScore > candidates[j].finalScore
	})

	hits := make([]KnowledgeHit, 0, len(candidates))
	for _, candidate := range candidates {
		meta := NormalizeKnowledgeMetadata(KnowledgeDocumentMetadata{
			DocType:  candidate.row.DocType,
			Audience: candidate.row.Audience,
			Intent:   candidate.row.Intent,
		})
		hits = append(hits, KnowledgeHit{
			TenantID:   candidate.row.TenantID,
			DocumentID: candidate.row.DocumentID,
			Title:      candidate.row.Title,
			SourcePath: candidate.row.SourcePath,
			DocType:    meta.DocType,
			Audience:   meta.Audience,
			Intent:     meta.Intent,
			ChunkIndex: candidate.row.ChunkIndex,
			Heading:    candidate.row.Heading,
			Body:       candidate.row.Body,
			SourceRef:  candidate.row.SourceRef,
			Score:      candidate.finalScore,
		})
	}

	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// BuildChunks 按 Markdown 标题切分文档并生成来源引用。
func (s *AgentKnowledgeService) BuildChunks(title, sourcePath, content string) []KnowledgeChunkDraft {
	lines := strings.Split(content, "\n")
	chunks := make([]KnowledgeChunkDraft, 0)

	var currentHeading string
	var bodyLines []string
	nextChunkIndex := 1

	// flush 将当前标题下累积的正文落成一个知识切片。
	flush := func() {
		body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
		if currentHeading == "" || body == "" {
			bodyLines = nil
			return
		}
		chunks = append(chunks, KnowledgeChunkDraft{
			ChunkIndex: nextChunkIndex,
			Heading:    currentHeading,
			Body:       body,
			SearchText: normalizeSearchText(currentHeading + " " + body),
			SourceRef:  fmt.Sprintf("%s#%d", title, nextChunkIndex),
		})
		nextChunkIndex++
		bodyLines = nil
	}

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if heading, ok := parseMarkdownHeading(line); ok {
			flush()
			currentHeading = heading
			continue
		}
		if currentHeading == "" {
			currentHeading = title
		}
		bodyLines = append(bodyLines, line)
	}
	flush()

	if len(chunks) == 0 && strings.TrimSpace(content) != "" {
		body := strings.TrimSpace(content)
		chunks = append(chunks, KnowledgeChunkDraft{
			ChunkIndex: 1,
			Heading:    title,
			Body:       body,
			SearchText: normalizeSearchText(title + " " + body),
			SourceRef:  fmt.Sprintf("%s#1", title),
		})
	}

	_ = sourcePath // 首版切片不直接消费 sourcePath，但保留参数与后续导入接口一致。
	return chunks
}

// SyncMarkdownDocuments 同步一批 Markdown 文档并重建对应切片。
func (s *AgentKnowledgeService) SyncMarkdownDocuments(ctx context.Context, tenantID uint, docs []MarkdownKnowledgeDocument) (KnowledgeSyncResult, error) {
	if s.repo == nil {
		return KnowledgeSyncResult{}, fmt.Errorf("agent knowledge repository is nil")
	}
	if tenantID == 0 {
		return KnowledgeSyncResult{}, fmt.Errorf("tenantID must be greater than 0")
	}

	ctx = tenantctx.WithTenantID(ctx, tenantID)
	result := KnowledgeSyncResult{}

	for _, doc := range docs {
		title := strings.TrimSpace(doc.Title)
		sourcePath := strings.TrimSpace(doc.SourcePath)
		content := strings.TrimSpace(doc.Content)
		meta := NormalizeKnowledgeMetadata(doc.Metadata)
		if title == "" || sourcePath == "" || content == "" {
			result.Skipped++
			continue
		}

		record := &model.AgentKnowledgeDocument{
			TenantID:    tenantID,
			Title:       title,
			SourcePath:  sourcePath,
			SourceType:  "markdown",
			DocType:     meta.DocType,
			Audience:    meta.Audience,
			Intent:      meta.Intent,
			ContentHash: hashKnowledgeContent(content),
			Status:      "active",
		}
		if err := s.repo.UpsertDocument(ctx, record); err != nil {
			return KnowledgeSyncResult{}, err
		}

		drafts := s.BuildChunks(title, sourcePath, content)
		chunks := make([]model.AgentKnowledgeChunk, 0, len(drafts))
		for _, draft := range drafts {
			chunks = append(chunks, model.AgentKnowledgeChunk{
				TenantID:   tenantID,
				DocumentID: record.ID,
				ChunkIndex: draft.ChunkIndex,
				Heading:    draft.Heading,
				Body:       draft.Body,
				SearchText: draft.SearchText,
				SourceRef:  draft.SourceRef,
				DocType:    meta.DocType,
				Audience:   meta.Audience,
				Intent:     meta.Intent,
			})
		}
		if err := s.repo.ReplaceChunks(ctx, tenantID, record.ID, chunks); err != nil {
			return KnowledgeSyncResult{}, err
		}

		result.DocumentsSynced++
		result.ChunksCreated += len(chunks)
	}

	return result, nil
}

// parseMarkdownHeading 解析 Markdown 标题行。
func parseMarkdownHeading(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
	if heading == "" {
		return "", false
	}
	return heading, true
}

// normalizeSearchText 统一清理文本，生成适合词项匹配的内容。
func normalizeSearchText(text string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(text) {
		if unicode.IsSpace(r) || strings.ContainsRune(".,;:!?，。；：！？()[]{}<>《》\"'`、", r) {
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}

// compactSearchText 将归一化文本压缩为无空格形式，便于短语匹配。
func compactSearchText(text string) string {
	return strings.ReplaceAll(normalizeSearchText(text), " ", "")
}

// splitSearchTerms 将问题拆成用于打分的检索词项。
func splitSearchTerms(text string) []string {
	normalized := normalizeSearchText(text)
	if normalized == "" {
		return nil
	}

	terms := make([]string, 0)
	for _, field := range strings.Fields(normalized) {
		terms = append(terms, expandSearchTerms(field)...)
	}

	if len(terms) == 0 {
		terms = append(terms, expandSearchTerms(normalized)...)
	}

	return uniqueSearchTerms(terms)
}

// buildKnowledgeQueryPlan 为一次检索构造原始问句与 alias 扩展后的词项集合。
func buildKnowledgeQueryPlan(query string) knowledgeQueryPlan {
	variants := expandKnowledgeQueryVariants(query)
	if len(variants) == 0 {
		return knowledgeQueryPlan{}
	}

	compactQueries := make([]string, 0, len(variants))
	queryTerms := make([]string, 0, len(variants)*4)
	for _, variant := range variants {
		compact := compactSearchText(variant)
		if compact == "" {
			continue
		}
		compactQueries = append(compactQueries, compact)
		queryTerms = append(queryTerms, splitSearchTerms(variant)...)
	}

	return knowledgeQueryPlan{
		CompactQueries: uniqueSearchTerms(compactQueries),
		QueryTerms:     uniqueSearchTerms(queryTerms),
	}
}

// expandKnowledgeQueryVariants 为真实口语化问法补同义表达，提升规则文档召回。
func expandKnowledgeQueryVariants(query string) []string {
	normalized := normalizeSearchText(query)
	if normalized == "" {
		return nil
	}

	variants := []string{normalized}
	compact := compactSearchText(normalized)
	for key, aliases := range retrievalAliases {
		if !strings.Contains(compact, compactSearchText(key)) {
			continue
		}
		variants = append(variants, aliases...)
	}
	return uniqueNormalizedTexts(variants)
}

// uniqueNormalizedTexts 对查询变体做归一化和去重。
func uniqueNormalizedTexts(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizeSearchText(value)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

// expandSearchTerms 为中文整句查询补充双字、三字词项，避免整句匹配导致全部得分为零。
func expandSearchTerms(text string) []string {
	compact := compactSearchText(text)
	if compact == "" {
		return nil
	}

	terms := []string{compact}
	if !containsHanRune(compact) {
		return terms
	}

	runes := []rune(compact)
	if len(runes) >= 2 {
		for i := 0; i <= len(runes)-2; i++ {
			terms = append(terms, string(runes[i:i+2]))
		}
	}
	if len(runes) >= 3 {
		for i := 0; i <= len(runes)-3; i++ {
			terms = append(terms, string(runes[i:i+3]))
		}
	}

	return terms
}

// uniqueSearchTerms 去重并丢弃过短噪声词项，保留首版词法检索所需的稳定集合。
func uniqueSearchTerms(terms []string) []string {
	seen := make(map[string]struct{}, len(terms))
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		term = compactSearchText(term)
		if term == "" {
			continue
		}
		if utf8.RuneCountInString(term) == 1 && containsHanRune(term) {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
}

// containsHanRune 判断词项中是否包含中文字符。
func containsHanRune(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// scoreKnowledgeLexical 根据标题、正文和搜索文本计算词法相关性得分。
func scoreKnowledgeLexical(row repository.AgentKnowledgeSearchRow, compactQueries []string, queryTerms []string) int {
	headingCompact := compactSearchText(row.Heading)
	bodyCompact := compactSearchText(row.Body)
	titleCompact := compactSearchText(row.Title)
	searchCompact := compactSearchText(row.SearchText)

	score := 0
	for _, compactQuery := range compactQueries {
		if compactQuery == "" {
			continue
		}
		if strings.Contains(headingCompact, compactQuery) {
			score += 12
		}
		if strings.Contains(titleCompact, compactQuery) {
			score += 10
		}
		if strings.Contains(searchCompact, compactQuery) || strings.Contains(bodyCompact, compactQuery) {
			score += 8
		}
	}

	for _, term := range queryTerms {
		compactTerm := compactSearchText(term)
		if compactTerm == "" {
			continue
		}
		if strings.Contains(headingCompact, compactTerm) {
			score += 4
		}
		if strings.Contains(titleCompact, compactTerm) {
			score += 3
		}
		if strings.Contains(searchCompact, compactTerm) || strings.Contains(bodyCompact, compactTerm) {
			score += 2
		}
	}

	return score
}

// scoreKnowledgeMetadata 用文档类型和意图为词法候选做轻量重排。
func scoreKnowledgeMetadata(row repository.AgentKnowledgeSearchRow, queryTerms []string) int {
	meta := NormalizeKnowledgeMetadata(KnowledgeDocumentMetadata{
		DocType:  row.DocType,
		Audience: row.Audience,
		Intent:   row.Intent,
	})

	score := 0
	switch meta.DocType {
	case "rule":
		score += 8
	case "faq":
		score += 6
	case "overview":
		score -= 2
	case "admin":
		score -= 5
	}

	switch meta.Audience {
	case "shared":
		score += 1
	case "admin":
		if !queryMatchesAnyHint(queryTerms, knowledgeAdminHints) {
			score -= 4
		}
	}

	if hints, ok := knowledgeIntentHints[meta.Intent]; ok {
		if queryMatchesAnyHint(queryTerms, hints) {
			score += 3
		} else if meta.Intent == "system" {
			score -= 1
		}
	}

	return score
}

// queryMatchesAnyHint 判断查询词项是否命中某组业务提示词。
func queryMatchesAnyHint(queryTerms []string, hints []string) bool {
	for _, hint := range hints {
		for _, expanded := range expandSearchTerms(hint) {
			for _, term := range queryTerms {
				if term == expanded || strings.Contains(term, expanded) || strings.Contains(expanded, term) {
					return true
				}
			}
		}
	}
	return false
}

// hashKnowledgeContent 计算知识文档内容的稳定哈希值。
func hashKnowledgeContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
