package agent

import (
	"strings"
	"testing"
)

// TestBuildKnowledgePromptSeparatesKnowledgeRulesFromToolFacts 验证 knowledge-only 提示词会明确规则边界。
func TestBuildKnowledgePromptSeparatesKnowledgeRulesFromToolFacts(t *testing.T) {
	prompt := buildKnowledgeOnlyPrompt(RetrievalResult{
		Hits: []KnowledgeHit{
			{
				Title:     "请假同步说明",
				Heading:   "同步失败处理",
				Body:      "同步失败不会直接覆盖已经生成的考勤快照。",
				SourceRef: "请假同步说明#3",
			},
		},
	})

	if !strings.Contains(prompt, "规则说明只能来自下面的知识片段") {
		t.Fatalf("prompt missing knowledge-only rule boundary: %q", prompt)
	}
	if !strings.Contains(prompt, "不要把实时数据或工具结果当成规则说明") {
		t.Fatalf("prompt missing tool/knowledge separation: %q", prompt)
	}
	if !strings.Contains(prompt, "请假同步说明#3") {
		t.Fatalf("prompt missing source ref: %q", prompt)
	}
}

// TestBuildMixedAnswerPromptRequiresRealtimeFirstThenRuleExplanation 验证 mixed 提示词会固定答案顺序。
func TestBuildMixedAnswerPromptRequiresRealtimeFirstThenRuleExplanation(t *testing.T) {
	prompt := buildMixedAnswerPrompt(RetrievalResult{
		Hits: []KnowledgeHit{
			{
				Title:     "考勤规则",
				Heading:   "迟到判定",
				Body:      "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef: "考勤规则#1",
			},
		},
	})

	if !strings.Contains(prompt, "先回答实时查询结果") {
		t.Fatalf("prompt missing realtime-first instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "再补充规则说明") {
		t.Fatalf("prompt missing rule-explanation instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "最后列出来源") {
		t.Fatalf("prompt missing source instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "考勤规则#1") {
		t.Fatalf("prompt missing knowledge source ref: %q", prompt)
	}
}
