package repository

import (
	"context"

	"schedule_server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GroupAttendanceSubscriptionRepository 群考勤推送订阅仓库
type GroupAttendanceSubscriptionRepository interface {
	Upsert(ctx context.Context, sub *model.GroupAttendanceSubscription) error
	SoftDelete(ctx context.Context, tenantID uint, conversationID string) error
	ListByTenantID(ctx context.Context, tenantID uint) ([]model.GroupAttendanceSubscription, error)
}

type groupAttendanceSubscriptionRepository struct {
	db *gorm.DB
}

func NewGroupAttendanceSubscriptionRepository(db *gorm.DB) GroupAttendanceSubscriptionRepository {
	return &groupAttendanceSubscriptionRepository{db: db}
}

func (r *groupAttendanceSubscriptionRepository) Upsert(ctx context.Context, sub *model.GroupAttendanceSubscription) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "conversation_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"group_name", "enabled_by_uid", "dept_ids_json", "deleted_at"}),
		}).Create(sub).Error
}

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
