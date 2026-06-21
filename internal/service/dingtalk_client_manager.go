package service

import (
	"context"
	"errors"
	"strings"
	"sync"

	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/dingtalk"
)

var (
	errTenantRequired = errors.New("钉钉租户: 缺少 tenant_id")
	errCorpRequired   = errors.New("钉钉租户: 缺少 corp_id")
)

type dingTalkEntry struct {
	tenant *model.Tenant
	client *dingtalk.Client
}

// DingTalkClientManager 按租户缓存钉钉 client（每个 tenant 一个 token 缓存域）。
//
// 说明：
// - client 内部已实现 access_token 自动刷新与缓存，因此这里按 tenant 维度复用 client 即可
// - tenant 配置变更后，可调用 Invalidate 失效缓存（下一次 Get 会重新加载并创建 client）
type DingTalkClientManager struct {
	tenantRepo repository.TenantRepository

	mu       sync.RWMutex
	byTenant map[uint]*dingTalkEntry
	byCorpID map[string]uint
}

func NewDingTalkClientManager(tenantRepo repository.TenantRepository) *DingTalkClientManager {
	return &DingTalkClientManager{
		tenantRepo: tenantRepo,
		byTenant:   make(map[uint]*dingTalkEntry),
		byCorpID:   make(map[string]uint),
	}
}

// FromContext 根据 request context 中的 tenant_id 获取对应的 tenant 与 client。
func (m *DingTalkClientManager) FromContext(ctx context.Context) (*model.Tenant, *dingtalk.Client, error) {
	tenantID, ok := tenantctx.TenantIDFrom(ctx)
	if !ok || tenantID == 0 {
		return nil, nil, errTenantRequired
	}
	return m.GetByTenantID(ctx, tenantID)
}

// GetByCorpID 根据 corp_id 获取 tenant 与 client（登录阶段常用）。
func (m *DingTalkClientManager) GetByCorpID(ctx context.Context, corpID string) (*model.Tenant, *dingtalk.Client, error) {
	corpID = strings.TrimSpace(corpID)
	if corpID == "" {
		return nil, nil, errCorpRequired
	}

	// 先尝试根据 corpID 找到已缓存的 tenantID
	m.mu.RLock()
	if tenantID, ok := m.byCorpID[corpID]; ok {
		if e := m.byTenant[tenantID]; e != nil {
			m.mu.RUnlock()
			return e.tenant, e.client, nil
		}
	}
	m.mu.RUnlock()

	tenant, err := m.tenantRepo.FindActiveByCorpID(ctx, corpID)
	if err != nil {
		return nil, nil, err
	}

	return m.getOrCreateByTenant(tenant)
}

// GetByTenantID 根据 tenant_id 获取 tenant 与 client（鉴权后接口常用）。
func (m *DingTalkClientManager) GetByTenantID(ctx context.Context, tenantID uint) (*model.Tenant, *dingtalk.Client, error) {
	if tenantID == 0 {
		return nil, nil, errTenantRequired
	}

	m.mu.RLock()
	if e := m.byTenant[tenantID]; e != nil {
		m.mu.RUnlock()
		return e.tenant, e.client, nil
	}
	m.mu.RUnlock()

	tenant, err := m.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}

	return m.getOrCreateByTenant(tenant)
}

// getOrCreateByTenant 返回指定租户的钉钉 client，若无缓存则创建。
func (m *DingTalkClientManager) getOrCreateByTenant(tenant *model.Tenant) (*model.Tenant, *dingtalk.Client, error) {
	if tenant == nil || tenant.ID == 0 {
		return nil, nil, repository.ErrTenantNotFound
	}

	// double-check：并发场景下避免重复创建
	m.mu.RLock()
	if e := m.byTenant[tenant.ID]; e != nil {
		m.mu.RUnlock()
		return e.tenant, e.client, nil
	}
	m.mu.RUnlock()

	client, err := dingtalk.NewClient(tenant.AppKey, tenant.AppSecret)
	if err != nil {
		return nil, nil, err
	}

	entry := &dingTalkEntry{
		tenant: tenant,
		client: client,
	}

	m.mu.Lock()
	// 再检查一次，避免另一协程刚创建
	if existing := m.byTenant[tenant.ID]; existing != nil {
		m.mu.Unlock()
		return existing.tenant, existing.client, nil
	}
	m.byTenant[tenant.ID] = entry
	if corpID := strings.TrimSpace(tenant.CorpID); corpID != "" {
		m.byCorpID[corpID] = tenant.ID
	}
	m.mu.Unlock()

	return entry.tenant, entry.client, nil
}

// InvalidateByTenantID 失效指定 tenant 的缓存（配置变更后使用）。
func (m *DingTalkClientManager) InvalidateByTenantID(tenantID uint) {
	if tenantID == 0 {
		return
	}
	m.mu.Lock()
	if e := m.byTenant[tenantID]; e != nil && e.tenant != nil {
		if corpID := strings.TrimSpace(e.tenant.CorpID); corpID != "" {
			delete(m.byCorpID, corpID)
		}
	}
	delete(m.byTenant, tenantID)
	m.mu.Unlock()
}

// InvalidateByCorpID 失效指定 corp_id 对应 tenant 的缓存（配置变更后使用）。
func (m *DingTalkClientManager) InvalidateByCorpID(corpID string) {
	corpID = strings.TrimSpace(corpID)
	if corpID == "" {
		return
	}
	m.mu.Lock()
	if tenantID, ok := m.byCorpID[corpID]; ok {
		delete(m.byCorpID, corpID)
		delete(m.byTenant, tenantID)
	}
	m.mu.Unlock()
}
