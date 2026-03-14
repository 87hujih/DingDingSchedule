package model

import "time"

// AgentCallLog AI 助手对话调用记录
type AgentCallLog struct {
	ID          uint      `gorm:"primaryKey"`
	TenantID    uint      `gorm:"not null;index"`
	UserID      uint      `gorm:"not null;index"`
	UserName    string    `gorm:"size:100"`
	ConvType    string    `gorm:"size:1"`    // "1"=单聊, "2"=群聊
	Question    string    `gorm:"type:text"` // 用户提问
	ToolsCalled string    `gorm:"size:500"`  // 调用的工具，逗号分隔
	Reply       string    `gorm:"type:text"` // 最终回复
	Rounds      int       // ReAct 轮数
	DurationMs  int64     // 总耗时（毫秒）
	Status      string    `gorm:"size:20"` // success / failed / timeout
	ErrorMsg    string    `gorm:"size:500"`
	CreatedAt   time.Time `gorm:"index"`
}

func (AgentCallLog) TableName() string { return "agent_call_logs" }
