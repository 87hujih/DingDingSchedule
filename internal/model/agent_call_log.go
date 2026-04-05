package model

import "time"

// AgentCallLog AI 助手对话调用记录
type AgentCallLog struct {
	ID                      uint   `gorm:"primaryKey"`
	TenantID                uint   `gorm:"not null;index"`
	UserID                  uint   `gorm:"not null;index"`
	UserName                string `gorm:"size:100"`
	ConvType                string `gorm:"size:1"`  // "1"=单聊, "2"=群聊
	QueryType               string `gorm:"size:20"` // tool / rag / mixed
	ConversationEvent       string `gorm:"size:32"`
	ActiveTaskType          string `gorm:"size:64"`
	TaskStatusBefore        string `gorm:"size:32"`
	TaskStatusAfter         string `gorm:"size:32"`
	DomainResult            string `gorm:"size:20"`
	DomainHint              string `gorm:"size:20"`
	PlanKind                string `gorm:"size:32"`
	KnowledgeStrength       string `gorm:"size:16"`
	PlannerReason           string `gorm:"size:64"`
	PlannerAction           string `gorm:"size:32"`
	PlannerConfidence       float64
	TaskID                  string `gorm:"size:64"`
	TaskKeepOpen            bool
	TaskSwitch              bool
	LastErrorCode           string `gorm:"size:64"`
	ShadowPlannerAction     string `gorm:"size:32"`
	ShadowPlannerMatched    bool
	AnswerMode              string `gorm:"size:32"`
	Question                string `gorm:"type:text"` // 用户提问
	ToolsCalled             string `gorm:"size:500"`  // 调用的工具，逗号分隔
	ToolCallCount           int
	Reply                   string `gorm:"type:text"` // 最终回复
	SourceRefs              string `gorm:"type:text"`
	RetrievalHitCount       int
	RetrievalCandidateCount int
	RetrievalTopRefs        string `gorm:"type:text"`
	RetrievalScores         string `gorm:"type:text"`
	FollowUpMatchedSlots    string `gorm:"type:text"`
	RetrievalFilteredReason string `gorm:"size:255"`
	KnowledgeDocTypes       string `gorm:"type:text"`
	RetrievalDurationMs     int64
	LLMDurationMs           int64
	Rounds                  int       // ReAct 轮数
	DurationMs              int64     // 总耗时（毫秒）
	Status                  string    `gorm:"size:20"` // success / failed / timeout
	ErrorMsg                string    `gorm:"size:500"`
	CreatedAt               time.Time `gorm:"index"`
}

// TableName 返回 Agent 调用日志表名。
func (AgentCallLog) TableName() string { return "agent_call_logs" }
