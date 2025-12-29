package repository

import (
	"context"
	"errors"
	"strings"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

var (
	// ErrTenantNotFound 租户不存在或被禁用
	ErrTenantNotFound = errors.New("没有找到租户")
)

// TenantRepository 租户仓库接口
type TenantRepository interface {
	FindActiveByCorpID(ctx context.Context, corpID string) (*model.Tenant, error)
	FindByID(ctx context.Context, id uint) (*model.Tenant, error)
}

type tenantRepository struct {
	db *gorm.DB
}

func NewTenantRepository(db *gorm.DB) TenantRepository {
	return &tenantRepository{db: db}
}

func (r *tenantRepository) FindActiveByCorpID(ctx context.Context, corpID string) (*model.Tenant, error) {
	corpID = strings.TrimSpace(corpID)
	if corpID == "" {
		return nil, ErrTenantNotFound
	}

	var tenant model.Tenant
	err := r.db.WithContext(ctx).
		Where("corp_id = ? AND status = ?", corpID, 1).
		First(&tenant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return &tenant, nil
}

func (r *tenantRepository) FindByID(ctx context.Context, id uint) (*model.Tenant, error) {
	if id == 0 {
		return nil, ErrTenantNotFound
	}

	var tenant model.Tenant
	err := r.db.WithContext(ctx).
		Where("id = ? AND status = ?", id, 1).
		First(&tenant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return &tenant, nil
}
