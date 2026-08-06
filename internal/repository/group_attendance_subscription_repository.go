package repository

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"schedule_server/internal/model"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GroupAttendanceSubscriptionRepository 群考勤推送订阅仓库
type GroupAttendanceSubscriptionRepository interface {
	Upsert(ctx context.Context, sub *model.GroupAttendanceSubscription) error
	SoftDelete(ctx context.Context, tenantID uint, conversationID string) error
	ApplyStart(ctx context.Context, sub *model.GroupAttendanceSubscription, businessKey string) (GroupSubscriptionMutationResult, error)
	ApplyCancel(ctx context.Context, tenantID uint, conversationID, businessKey string) (GroupSubscriptionMutationResult, error)
	ListByTenantID(ctx context.Context, tenantID uint) ([]model.GroupAttendanceSubscription, error)
	ListPushEnabledByTenantID(ctx context.Context, tenantID uint) ([]model.GroupAttendanceSubscription, error)
	FindByConversationID(ctx context.Context, tenantID uint, conversationID string) (*model.GroupAttendanceSubscription, error)
}

type GroupSubscriptionWriteEffect string

const (
	GroupSubscriptionCreated   GroupSubscriptionWriteEffect = "created"
	GroupSubscriptionUpdated   GroupSubscriptionWriteEffect = "updated"
	GroupSubscriptionNoOp      GroupSubscriptionWriteEffect = "no_op"
	GroupSubscriptionCancelled GroupSubscriptionWriteEffect = "cancelled"
)

type GroupSubscriptionMutationResult struct {
	Effect       GroupSubscriptionWriteEffect
	Subscription *model.GroupAttendanceSubscription
}

type groupAttendanceSubscriptionRepository struct {
	db *gorm.DB
}

func NewGroupAttendanceSubscriptionRepository(db *gorm.DB) GroupAttendanceSubscriptionRepository {
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

func (r *groupAttendanceSubscriptionRepository) ApplyStart(
	ctx context.Context,
	sub *model.GroupAttendanceSubscription,
	businessKey string,
) (GroupSubscriptionMutationResult, error) {
	if sub == nil || sub.TenantID == 0 || strings.TrimSpace(sub.ConversationID) == "" {
		return GroupSubscriptionMutationResult{}, errors.New("repository: subscription start command is incomplete")
	}
	tenantID, err := tenantIDFromCtx(ctx)
	if err != nil {
		return GroupSubscriptionMutationResult{}, err
	}
	if tenantID != sub.TenantID {
		return GroupSubscriptionMutationResult{}, errors.New("repository: subscription tenant does not match context")
	}
	if err := validateAgentWriteBusinessKey(businessKey); err != nil {
		return GroupSubscriptionMutationResult{}, err
	}
	canonicalDeptIDs, err := canonicalSubscriptionDeptIDsJSON(sub.DeptIDsJSON)
	if err != nil {
		return GroupSubscriptionMutationResult{}, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		mutation, applyErr := r.applyStartOnce(ctx, sub, canonicalDeptIDs, businessKey)
		if applyErr == nil {
			return mutation, nil
		}
		if attempt == 2 || !retryableGroupSubscriptionTransactionError(applyErr) {
			return GroupSubscriptionMutationResult{}, applyErr
		}
		select {
		case <-ctx.Done():
			return GroupSubscriptionMutationResult{}, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 5 * time.Millisecond):
		}
	}
	return GroupSubscriptionMutationResult{}, errors.New("repository: subscription transaction retry exhausted")
}

func (r *groupAttendanceSubscriptionRepository) applyStartOnce(
	ctx context.Context,
	sub *model.GroupAttendanceSubscription,
	canonicalDeptIDs string,
	businessKey string,
) (GroupSubscriptionMutationResult, error) {
	var mutation GroupSubscriptionMutationResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.GroupAttendanceSubscription
		loadErr := tx.Unscoped().
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND conversation_id = ?", sub.TenantID, strings.TrimSpace(sub.ConversationID)).
			First(&current).Error
		if errors.Is(loadErr, gorm.ErrRecordNotFound) {
			next := *sub
			next.ConversationID = strings.TrimSpace(next.ConversationID)
			next.DeptIDsJSON = canonicalDeptIDs
			if err := tx.Create(&next).Error; err != nil {
				return err
			}
			mutation = GroupSubscriptionMutationResult{
				Effect:       GroupSubscriptionCreated,
				Subscription: &next,
			}
			return recordAgentWriteLedger(tx, sub.TenantID, businessKey, "subscription.start", mutation.Effect)
		}
		if loadErr != nil {
			return loadErr
		}

		currentDeptIDs, err := canonicalSubscriptionDeptIDsJSON(current.DeptIDsJSON)
		if err != nil {
			return fmt.Errorf("repository: stored subscription department scope is invalid: %w", err)
		}
		if !current.DeletedAt.Valid && currentDeptIDs == canonicalDeptIDs {
			mutation = GroupSubscriptionMutationResult{
				Effect:       GroupSubscriptionNoOp,
				Subscription: &current,
			}
			return recordAgentWriteLedger(tx, sub.TenantID, businessKey, "subscription.start", mutation.Effect)
		}

		effect := GroupSubscriptionUpdated
		if current.DeletedAt.Valid {
			effect = GroupSubscriptionCreated
		}
		if err := tx.Unscoped().
			Model(&model.GroupAttendanceSubscription{}).
			Where("id = ?", current.ID).
			Updates(map[string]any{
				"group_name":     sub.GroupName,
				"enabled_by_uid": sub.EnabledByUID,
				"dept_ids_json":  canonicalDeptIDs,
				"deleted_at":     nil,
			}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().First(&current, current.ID).Error; err != nil {
			return err
		}
		mutation = GroupSubscriptionMutationResult{
			Effect:       effect,
			Subscription: &current,
		}
		return recordAgentWriteLedger(tx, sub.TenantID, businessKey, "subscription.start", mutation.Effect)
	})
	if err != nil {
		return GroupSubscriptionMutationResult{}, err
	}
	return mutation, nil
}

func retryableGroupSubscriptionTransactionError(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1062, 1205, 1213:
		return true
	default:
		return false
	}
}

func (r *groupAttendanceSubscriptionRepository) ApplyCancel(
	ctx context.Context,
	tenantID uint,
	conversationID string,
	businessKey string,
) (GroupSubscriptionMutationResult, error) {
	contextTenantID, err := tenantIDFromCtx(ctx)
	if err != nil {
		return GroupSubscriptionMutationResult{}, err
	}
	if tenantID == 0 || contextTenantID != tenantID {
		return GroupSubscriptionMutationResult{}, errors.New("repository: subscription tenant does not match context")
	}
	if err := validateAgentWriteBusinessKey(businessKey); err != nil {
		return GroupSubscriptionMutationResult{}, err
	}
	var mutation GroupSubscriptionMutationResult
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.
			Where("tenant_id = ? AND conversation_id = ?", tenantID, strings.TrimSpace(conversationID)).
			Delete(&model.GroupAttendanceSubscription{})
		if result.Error != nil {
			return result.Error
		}
		mutation.Effect = GroupSubscriptionNoOp
		if result.RowsAffected == 1 {
			mutation.Effect = GroupSubscriptionCancelled
		}
		return recordAgentWriteLedger(tx, tenantID, businessKey, "subscription.cancel", mutation.Effect)
	})
	if err != nil {
		return GroupSubscriptionMutationResult{}, err
	}
	return mutation, nil
}

func validateAgentWriteBusinessKey(key string) error {
	key = strings.TrimSpace(key)
	if len(key) != 64 {
		return errors.New("repository: agent write business key must be 64 hex characters")
	}
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 || key != strings.ToLower(key) {
		return errors.New("repository: agent write business key is invalid")
	}
	return nil
}

func recordAgentWriteLedger(
	tx *gorm.DB,
	tenantID uint,
	businessKey string,
	operation string,
	effect GroupSubscriptionWriteEffect,
) error {
	now := time.Now().UTC()
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "business_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"operation":    operation,
			"write_effect": string(effect),
			"updated_at":   now,
		}),
	}).Create(&model.AgentWriteLedger{
		TenantID:    tenantID,
		BusinessKey: businessKey,
		Operation:   operation,
		WriteEffect: string(effect),
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error
}

func canonicalSubscriptionDeptIDsJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return "", nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return "", fmt.Errorf("decode department ids: %w", err)
	}
	if len(ids) == 0 {
		return "", nil
	}
	unique := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return "", fmt.Errorf("department id must be positive: %d", id)
		}
		unique[id] = struct{}{}
	}
	ids = ids[:0]
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	payload, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("encode department ids: %w", err)
	}
	return string(payload), nil
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
