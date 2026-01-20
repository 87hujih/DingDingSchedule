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
	// ListActive 获取所有活跃的租户
	ListActive(ctx context.Context) ([]model.Tenant, error)
}

type tenantRepository struct {
	db *gorm.DB
}

// NewTenantRepository 构建一个新的 TenantRepository 实现
func NewTenantRepository(db *gorm.DB) TenantRepository {
	return &tenantRepository{db: db}
}

// FindActiveByCorpID 根据 corpID 查找已启用的租户
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

// FindByID 根据 ID 查找已启用的租户
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

// ListActive 获取所有活跃的租户
func (r *tenantRepository) ListActive(ctx context.Context) ([]model.Tenant, error) {
	var tenants []model.Tenant
	err := r.db.WithContext(ctx).
		Where("status = ?", 1).
		Order("id ASC").
		Find(&tenants).Error
	if err != nil {
		return nil, err
	}
	return tenants, nil
}
