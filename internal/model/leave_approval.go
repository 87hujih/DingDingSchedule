package model

import (
	"time"

	"gorm.io/gorm"
)

// LeaveApproval 请假审批落库记录（用于从审批表单中获取请假事由/理由，并支持审批中状态）。
//
// 说明：
// - 该表是“审批实例(process_instance_id)”的本地镜像，允许重复/乱序回调，通过 (tenant_id, process_instance_id) 幂等 upsert。
// - start_at/end_at：请假时间段（用于与课节时间窗口做 overlap 查询）。
// - reason：请假事由/理由（最终映射到接口返回的 remark）。
type LeaveApproval struct {
	ID       uint `gorm:"primaryKey" json:"id"`
	TenantID uint `gorm:"not null;uniqueIndex:uniq_tenant_pi;index;index:idx_tenant_user_time,priority:1;index:idx_tenant_ding_time,priority:1" json:"tenant_id"`

	// 钉钉审批实例唯一标识（每次提交生成一个新的实例ID）
	ProcessInstanceID string `gorm:"size:128;not null;uniqueIndex:uniq_tenant_pi;index" json:"process_instance_id"`
	// 流程编码（审批模板/流程定义）
	ProcessCode string `gorm:"size:128;index" json:"process_code"`

	// 发起人（或请假人）标识：尽量同时保存 ding_user_id 与本地 user_id，便于查询与关联
	DingUserID string `gorm:"size:64;index:idx_tenant_ding_time,priority:2" json:"ding_user_id"`
	UserID     uint   `gorm:"index:idx_tenant_user_time,priority:2" json:"user_id"`
	UserName   string `gorm:"size:128" json:"user_name"` // 请假人姓名（快照）

	// 请假时间范围
	StartAt time.Time `gorm:"not null;index:idx_tenant_user_time,priority:3;index:idx_tenant_ding_time,priority:3" json:"start_at"`
	EndAt   time.Time `gorm:"not null;index:idx_tenant_user_time,priority:4;index:idx_tenant_ding_time,priority:4" json:"end_at"`

	// 业务字段
	LeaveType     string `gorm:"size:64" json:"leave_type"`
	Reason        string `gorm:"type:text" json:"reason"`
	ApproveStatus string `gorm:"size:32;index" json:"approve_status"` // RUNNING/COMPLETED/TERMINATED...
	Result        string `gorm:"size:32;index" json:"result"`         // agree/refuse/...

	// 排障字段：保留原始 JSON（可选）
	RawInstanceJSON string `gorm:"type:longtext" json:"-"`
	RawFormJSON     string `gorm:"type:longtext" json:"-"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (*LeaveApproval) TableName() string { return "leave_approvals" }


