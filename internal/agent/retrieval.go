package agent

import (
	"context"
	"strings"
)

const defaultKnowledgeTopK = 4

const (
	retrievalFilteredReasonKnowledgeUnavailable = "knowledge_unavailable"
	retrievalFilteredReasonNoHits               = "no_hits"
)

// retrieveKnowledge 按租户和问题执行知识检索，并整理为结构化结果。
func (a *Agent) retrieveKnowledge(ctx context.Context, tenantID uint, question string) (RetrievalResult, error) {
	if a.deps.Knowledge == nil || tenantID == 0 {
		return RetrievalResult{FilteredReason: retrievalFilteredReasonKnowledgeUnavailable}, nil
	}

	hits, err := a.deps.Knowledge.Search(ctx, tenantID, question, defaultKnowledgeTopK)
	if err != nil {
		return RetrievalResult{}, err
	}

	result := RetrievalResult{
		Hits:              hits,
		CandidateCount:    len(hits),
		TopRefs:           collectKnowledgeSourceRefs(hits),
		TopScores:         collectKnowledgeScores(hits),
		KnowledgeDocTypes: collectKnowledgeDocTypes(hits),
	}
	if len(hits) == 0 {
		result.FilteredReason = retrievalFilteredReasonNoHits
	}
	return result, nil
}

// topKnowledgeScore 返回本次检索命中的最高分。
func topKnowledgeScore(result RetrievalResult) int {
	if len(result.TopScores) > 0 {
		return result.TopScores[0]
	}
	if len(result.Hits) > 0 {
		return result.Hits[0].Score
	}
	return 0
}

func classifyKnowledgeStrength(result RetrievalResult) KnowledgeStrength {
	if len(result.Hits) == 0 {
		return knowledgeStrengthNone
	}
	if topKnowledgeScore(result) >= retrievalStrongScoreThreshold {
		return knowledgeStrengthStrong
	}
	return knowledgeStrengthWeak
}

// collectKnowledgeSourceRefs 提取去重后的来源引用，用于日志和评测。
func collectKnowledgeSourceRefs(hits []KnowledgeHit) []string {
	sources := make([]string, 0, len(hits))
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		source := strings.TrimSpace(hit.SourceRef)
		if source == "" {
			source = strings.TrimSpace(hit.Title)
		}
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		sources = append(sources, source)
	}
	return sources
}

// collectKnowledgeScores 提取检索命中的分数序列。
func collectKnowledgeScores(hits []KnowledgeHit) []int {
	scores := make([]int, 0, len(hits))
	for _, hit := range hits {
		scores = append(scores, hit.Score)
	}
	return scores
}

// collectKnowledgeDocTypes 提取去重后的知识文档类型。
func collectKnowledgeDocTypes(hits []KnowledgeHit) []string {
	docTypes := make([]string, 0, len(hits))
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		docType := strings.TrimSpace(hit.DocType)
		if docType == "" {
			continue
		}
		if _, ok := seen[docType]; ok {
			continue
		}
		seen[docType] = struct{}{}
		docTypes = append(docTypes, docType)
	}
	return docTypes
}
