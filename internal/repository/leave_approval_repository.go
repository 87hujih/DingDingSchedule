package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LeaveApprovalRepository 请假审批落库仓库
type LeaveApprovalRepository interface {
	// UpsertByProcessInstanceID 基于 (tenant_id, process_instance_id) 幂等写入（插入或更新）。
	UpsertByProcessInstanceID(ctx context.Context, rec *model.LeaveApproval) error

	// ListOverlappingByDingUserIDs 查询一批用户在时间窗口内的请假审批记录（不做状态过滤，由上层决定是否计入）。
	ListOverlappingByDingUserIDs(ctx context.Context, dingUserIDs []string, startAt, endAt time.Time) ([]model.LeaveApproval, error)

	// ListOverlappingByUserID 查询某本地用户在时间窗口内的请假审批记录
	ListOverlappingByUserID(ctx context.Context, userID uint, startAt, endAt time.Time) ([]model.LeaveApproval, error)

	// ListApprovedByUserIDs 查询一批本地用户在时间窗口内计入考勤的请假记录。
	// 口径：
	// - 审批中、审批通过、结果缺失的历史记录：计入请假
	// - 审批拒绝、已撤销：不计入请假
	ListApprovedByUserIDs(ctx context.Context, userIDs []uint, startAt, endAt time.Time) ([]model.LeaveApproval, error)
}

type leaveApprovalRepository struct {
	db *gorm.DB
}

func NewLeaveApprovalRepository(db *gorm.DB) LeaveApprovalRepository {
	return &leaveApprovalRepository{db: db}
}

func (r *leaveApprovalRepository) UpsertByProcessInstanceID(ctx context.Context, rec *model.LeaveApproval) error {
	if rec == nil {
		return errors.New("repository: leave approval 为空")
	}
	rec.ProcessInstanceID = strings.TrimSpace(rec.ProcessInstanceID)
	if rec.ProcessInstanceID == "" {
		return errors.New("repository: process_instance_id 为空")
	}
	// 注意：tenant_id 由 TenantScopePlugin 从 ctx 强制写入/隔离，这里不显式校验 tenant_id。

	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "process_instance_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"process_code",
				"ding_user_id",
				"user_id",
				"start_at",
				"end_at",
				"leave_type",
				"reason",
				"approve_status",
				"result",
				"raw_instance_json",
				"raw_form_json",
				"updated_at",
			}),
		}).
		Create(rec).Error
}

func (r *leaveApprovalRepository) ListOverlappingByDingUserIDs(
	ctx context.Context,
	dingUserIDs []string,
	startAt, endAt time.Time,
) ([]model.LeaveApproval, error) {
	clean := make([]string, 0, len(dingUserIDs))
	seen := make(map[string]struct{}, len(dingUserIDs))
	for _, id := range dingUserIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return []model.LeaveApproval{}, nil
	}
	if endAt.Before(startAt) {
		return []model.LeaveApproval{}, nil
	}

	var out []model.LeaveApproval
	err := r.db.WithContext(ctx).
		Model(&model.LeaveApproval{}).
		Where("ding_user_id IN ?", clean).
		Where("start_at < ? AND end_at > ?", endAt, startAt).
		Order("start_at ASC, end_at ASC, id ASC").
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *leaveApprovalRepository) ListOverlappingByUserID(ctx context.Context, userID uint, startAt, endAt time.Time) ([]model.LeaveApproval, error) {
	if userID == 0 || endAt.Before(startAt) {
		return []model.LeaveApproval{}, nil
	}
	var out []model.LeaveApproval
	err := r.db.WithContext(ctx).
		Model(&model.LeaveApproval{}).
		Where("user_id = ?", userID).
		Where("start_at < ? AND end_at > ?", endAt, startAt).
		Order("start_at ASC, end_at ASC, id ASC").
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListApprovedByUserIDs 查询一批本地用户在时间窗口内计入考勤的请假记录。
func (r *leaveApprovalRepository) ListApprovedByUserIDs(ctx context.Context, userIDs []uint, startAt, endAt time.Time) ([]model.LeaveApproval, error) {
	if len(userIDs) == 0 || endAt.Before(startAt) {
		return []model.LeaveApproval{}, nil
	}

	var out []model.LeaveApproval
	err := r.db.WithContext(ctx).
		Model(&model.LeaveApproval{}).
		Where("user_id IN ?", userIDs).
		Where("start_at < ? AND end_at > ?", endAt, startAt).
		Where("COALESCE(result, '') NOT IN ?", []string{"refuse", "cancel"}).
		Order("start_at ASC, end_at ASC, id ASC").
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}
