package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GroupAttendanceSubscriptionRepository 群考勤推送订阅仓库
type GroupAttendanceSubscriptionRepository interface {
	Upsert(ctx context.Context, sub *model.GroupAttendanceSubscription) error
	SoftDelete(ctx context.Context, tenantID uint, conversationID string) error
	ListByTenantID(ctx context.Context, tenantID uint) ([]model.GroupAttendanceSubscription, error)
	ListPushEnabledByTenantID(ctx context.Context, tenantID uint) ([]model.GroupAttendanceSubscription, error)
	FindByConversationID(ctx context.Context, tenantID uint, conversationID string) (*model.GroupAttendanceSubscription, error)
}

type IdempotentGroupAttendanceSubscriptionRepository interface {
	GroupAttendanceSubscriptionRepository
	ExecuteSubscriptionStart(ctx context.Context, req SubscriptionStartExecution) (*model.AgentOperationExecution, error)
	ExecuteSubscriptionCancel(ctx context.Context, req SubscriptionCancelExecution) (*model.AgentOperationExecution, error)
}

type SubscriptionStartExecution struct {
	TenantID                               uint
	BusinessKey, ConversationID, GroupName string
	EnabledByUID                           uint
	DeptIDsJSON                            string
}
type SubscriptionCancelExecution struct {
	TenantID                    uint
	BusinessKey, ConversationID string
}

type groupAttendanceSubscriptionRepository struct {
	db *gorm.DB
}

var errAgentOperationAlreadyClaimed = errors.New("agent operation already claimed")

func NewGroupAttendanceSubscriptionRepository(db *gorm.DB) IdempotentGroupAttendanceSubscriptionRepository {
	return &groupAttendanceSubscriptionRepository{db: db}
}

// Upsert is idempotent for subscription.start because the model declares a
// unique tenant_id + conversation_id key and this write uses ON CONFLICT.
func (r *groupAttendanceSubscriptionRepository) Upsert(ctx context.Context, sub *model.GroupAttendanceSubscription) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "conversation_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"group_name", "enabled_by_uid", "dept_ids_json", "deleted_at"}),
		}).Create(sub).Error
}

// SoftDelete is idempotent for subscription.cancel; deleting an already
// deleted or missing subscription is a no-op at the repository boundary.
func (r *groupAttendanceSubscriptionRepository) SoftDelete(ctx context.Context, tenantID uint, conversationID string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).
		Delete(&model.GroupAttendanceSubscription{}).Error
}

func (r *groupAttendanceSubscriptionRepository) ListByTenantID(ctx context.Context, tenantID uint) ([]model.GroupAttendanceSubscription, error) {
	var subs []model.GroupAttendanceSubscription
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Find(&subs).Error
	return subs, err
}

func (r *groupAttendanceSubscriptionRepository) ListPushEnabledByTenantID(ctx context.Context, tenantID uint) ([]model.GroupAttendanceSubscription, error) {
	var subs []model.GroupAttendanceSubscription
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND push_enabled = ?", tenantID, true).
		Find(&subs).Error
	return subs, err
}

func (r *groupAttendanceSubscriptionRepository) FindByConversationID(ctx context.Context, tenantID uint, conversationID string) (*model.GroupAttendanceSubscription, error) {
	var sub model.GroupAttendanceSubscription
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).
		First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sub, err
}

func (r *groupAttendanceSubscriptionRepository) ExecuteSubscriptionStart(ctx context.Context, req SubscriptionStartExecution) (*model.AgentOperationExecution, error) {
	return r.executeSubscriptionWrite(ctx, req.TenantID, req.BusinessKey, req.ConversationID, "subscription.start", "subscription.cancel", func(tx *gorm.DB) (string, map[string]any, error) {
		var current model.GroupAttendanceSubscription
		err := tx.Unscoped().Where("tenant_id = ? AND conversation_id = ?", req.TenantID, req.ConversationID).First(&current).Error
		effect := model.AgentWriteEffectCreated
		same := false
		if err == nil {
			same = !current.DeletedAt.Valid && current.DeptIDsJSON == req.DeptIDsJSON
			if same {
				effect = model.AgentWriteEffectNoOp
			} else {
				effect = model.AgentWriteEffectUpdated
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil, err
		}
		if !same {
			sub := &model.GroupAttendanceSubscription{TenantID: req.TenantID, ConversationID: req.ConversationID, GroupName: req.GroupName, EnabledByUID: req.EnabledByUID, DeptIDsJSON: req.DeptIDsJSON}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "conversation_id"}}, DoUpdates: clause.AssignmentColumns([]string{"group_name", "enabled_by_uid", "dept_ids_json", "deleted_at"})}).Create(sub).Error; err != nil {
				return "", nil, err
			}
		}
		pushEnabled := true
		if err == nil {
			pushEnabled = current.PushEnabled
		}
		return effect, map[string]any{"push_enabled": pushEnabled}, nil
	})
}

func (r *groupAttendanceSubscriptionRepository) ExecuteSubscriptionCancel(ctx context.Context, req SubscriptionCancelExecution) (*model.AgentOperationExecution, error) {
	return r.executeSubscriptionWrite(ctx, req.TenantID, req.BusinessKey, req.ConversationID, "subscription.cancel", "subscription.start", func(tx *gorm.DB) (string, map[string]any, error) {
		result := tx.Where("tenant_id = ? AND conversation_id = ?", req.TenantID, req.ConversationID).Delete(&model.GroupAttendanceSubscription{})
		if result.Error != nil {
			return "", nil, result.Error
		}
		if result.RowsAffected == 0 {
			return model.AgentWriteEffectNoOp, nil, nil
		}
		return model.AgentWriteEffectCancelled, nil, nil
	})
}

func (r *groupAttendanceSubscriptionRepository) executeSubscriptionWrite(ctx context.Context, tenantID uint, businessKey, conversationID, operation, oppositeOperation string, mutate func(*gorm.DB) (string, map[string]any, error)) (*model.AgentOperationExecution, error) {
	if tenantID == 0 || strings.TrimSpace(businessKey) == "" || strings.TrimSpace(conversationID) == "" {
		return nil, fmt.Errorf("invalid agent operation identity")
	}
	for attempt := 0; attempt < 5; attempt++ {
		var result *model.AgentOperationExecution
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			execution := &model.AgentOperationExecution{TenantID: tenantID, BusinessKey: businessKey, ConversationID: conversationID, Operation: operation, Status: model.AgentOperationStatusExecuting, WriteEffect: model.AgentWriteEffectNoOp, ResultJSON: `{}`}
			claim := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "business_key"}}, DoNothing: true}).Create(execution)
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected == 0 {
				return errAgentOperationAlreadyClaimed
			}
			effect, extra, err := mutate(tx)
			if err != nil {
				return err
			}
			if err := tx.Where("tenant_id = ? AND conversation_id = ? AND operation = ?", tenantID, conversationID, oppositeOperation).Delete(&model.AgentOperationExecution{}).Error; err != nil {
				return err
			}
			if operation == "subscription.start" {
				if err := tx.Where("tenant_id = ? AND conversation_id = ? AND operation = ? AND business_key <> ?", tenantID, conversationID, operation, businessKey).Delete(&model.AgentOperationExecution{}).Error; err != nil {
					return err
				}
			}
			payloadData := map[string]any{"operation": operation, "write_effect": effect}
			for key, value := range extra {
				payloadData[key] = value
			}
			payload, err := json.Marshal(payloadData)
			if err != nil {
				return err
			}
			execution.Status = model.AgentOperationStatusSucceeded
			execution.WriteEffect = effect
			execution.ResultJSON = string(payload)
			if err := tx.Save(execution).Error; err != nil {
				return err
			}
			result = execution
			return nil
		})
		if err == nil {
			return result, nil
		}
		if errors.Is(err, errAgentOperationAlreadyClaimed) {
			existing, loadErr := NewAgentOperationExecutionRepository(r.db).FindSucceeded(ctx, tenantID, businessKey)
			if loadErr != nil && !isRetryableSubscriptionWriteError(loadErr) {
				return nil, loadErr
			}
			if existing != nil {
				return existing, nil
			}
			if err := waitSubscriptionRetry(ctx, time.Duration(attempt+1)*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		if !isRetryableSubscriptionWriteError(err) {
			return nil, err
		}
		existing, loadErr := NewAgentOperationExecutionRepository(r.db).FindSucceeded(ctx, tenantID, businessKey)
		if loadErr != nil && !isRetryableSubscriptionWriteError(loadErr) {
			return nil, loadErr
		}
		if existing != nil {
			return existing, nil
		}
		if err := waitSubscriptionRetry(ctx, time.Duration(attempt+1)*time.Millisecond); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("subscription write contention exceeded retries")
}

func waitSubscriptionRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableSubscriptionWriteError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate") || strings.Contains(message, "locked") || strings.Contains(message, "busy") || strings.Contains(message, "deadlock") || strings.Contains(message, "serialization failure") || strings.Contains(message, "lock wait timeout")
}
