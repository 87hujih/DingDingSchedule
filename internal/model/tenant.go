package model

import "time"

// Tenant 企业租户（一个 corp_id 对应一套钉钉应用凭证）
type Tenant struct {
	ID     uint   `gorm:"primaryKey" json:"id"`
	CorpID string `gorm:"size:64;not null;uniqueIndex" json:"corp_id"` // 企业标识
	Name   string `gorm:"size:128" json:"name"`                        // 企业名称

	AppKey    string `gorm:"size:100;not null" json:"app_key"`
	AppSecret string `gorm:"size:255;not null" json:"app_secret"`
	AgentID   string `gorm:"size:100;not null" json:"agent_id"`

	Status    int       `gorm:"default:1" json:"status"` // 1启用 0禁用
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Tenant) TableName() string { return "tenants" }
