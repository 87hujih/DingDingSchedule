package service

import (
	"context"
	"sync"

	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

// StreamClientManager 管理多租户的 Stream 客户端
type StreamClientManager struct {
	tenantRepo repository.TenantRepository
	logger     *zap.SugaredLogger

	mu      sync.RWMutex
	clients map[uint]*streamClientEntry // tenantID -> client entry
	cancels map[uint]context.CancelFunc // tenantID -> cancel function
}

type streamClientEntry struct {
	tenant *model.Tenant
	client *dingtalk.StreamClient
}

// NewStreamClientManager 创建 Stream 客户端管理器
func NewStreamClientManager(tenantRepo repository.TenantRepository, logger *zap.SugaredLogger) *StreamClientManager {
	return &StreamClientManager{
		tenantRepo: tenantRepo,
		logger:     logger,
		clients:    make(map[uint]*streamClientEntry),
		cancels:    make(map[uint]context.CancelFunc),
	}
}

// StartAll 启动所有活跃租户的 Stream 客户端
func (m *StreamClientManager) StartAll(ctx context.Context, eventHandler func(ctx context.Context, corpID, processInstanceID, eventType string) error) error {
	// 从数据库获取所有活跃租户
	tenants, err := m.tenantRepo.ListActive(ctx)
	if err != nil {
		return err
	}

	if len(tenants) == 0 {
		m.logger.Warn("没有找到活跃的租户，跳过 Stream 客户端启动")
		return nil
	}

	m.logger.Infof("准备启动 %d 个租户的 Stream 客户端", len(tenants))

	// 为每个租户启动 Stream 客户端
	for _, tenant := range tenants {
		if err := m.StartForTenant(ctx, &tenant, eventHandler); err != nil {
			m.logger.Errorw("启动租户 Stream 客户端失败",
				"tenantID", tenant.ID,
				"corpID", tenant.CorpID,
				"err", err,
			)
			// 继续启动其他租户的客户端
			continue
		}
	}

	return nil
}

// StartForTenant 为指定租户启动 Stream 客户端
func (m *StreamClientManager) StartForTenant(parentCtx context.Context, tenant *model.Tenant, eventHandler func(ctx context.Context, corpID, processInstanceID, eventType string) error) error {
	if tenant == nil || tenant.ID == 0 {
		return nil
	}

	// 检查是否已经启动
	m.mu.RLock()
	if _, exists := m.clients[tenant.ID]; exists {
		m.mu.RUnlock()
		m.logger.Infow("租户 Stream 客户端已存在，跳过启动",
			"tenantID", tenant.ID,
			"corpID", tenant.CorpID,
		)
		return nil
	}
	m.mu.RUnlock()

	// 验证配置
	if tenant.AppKey == "" || tenant.AppSecret == "" {
		m.logger.Warnw("租户缺少 AppKey/AppSecret，跳过 Stream 客户端启动",
			"tenantID", tenant.ID,
			"corpID", tenant.CorpID,
		)
		return nil
	}

	// 创建 Stream 客户端
	streamClient := dingtalk.NewStreamClient(tenant.AppKey, tenant.AppSecret, tenant.CorpID, m.logger)
	streamClient.SetBpmsEventHandler(eventHandler)

	// 创建可取消的 context
	ctx, cancel := context.WithCancel(parentCtx)

	// 保存到管理器
	m.mu.Lock()
	m.clients[tenant.ID] = &streamClientEntry{
		tenant: tenant,
		client: streamClient,
	}
	m.cancels[tenant.ID] = cancel
	m.mu.Unlock()

	// 在独立 goroutine 中启动客户端
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.logger.Errorw("租户 Stream 客户端 goroutine panic 已捕获",
					"tenantID", tenant.ID,
					"corpID", tenant.CorpID,
					"panic", r,
				)
			}
		}()

		m.logger.Infow("启动租户 Stream 客户端",
			"tenantID", tenant.ID,
			"corpID", tenant.CorpID,
			"appKey", tenant.AppKey,
		)

		if err := streamClient.Start(ctx); err != nil && err != context.Canceled {
			m.logger.Errorw("租户 Stream 客户端运行失败",
				"tenantID", tenant.ID,
				"corpID", tenant.CorpID,
				"err", err,
			)
		}

		m.logger.Infow("租户 Stream 客户端已停止",
			"tenantID", tenant.ID,
			"corpID", tenant.CorpID,
		)
	}()

	return nil
}

// StopForTenant 停止指定租户的 Stream 客户端
func (m *StreamClientManager) StopForTenant(tenantID uint) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cancel, exists := m.cancels[tenantID]; exists {
		cancel()
		delete(m.cancels, tenantID)
		delete(m.clients, tenantID)
		m.logger.Infow("已停止租户 Stream 客户端", "tenantID", tenantID)
	}
}

// StopAll 停止所有 Stream 客户端
func (m *StreamClientManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Infof("正在停止 %d 个租户的 Stream 客户端", len(m.cancels))

	for tenantID, cancel := range m.cancels {
		cancel()
		m.logger.Infow("已停止租户 Stream 客户端", "tenantID", tenantID)
	}

	m.clients = make(map[uint]*streamClientEntry)
	m.cancels = make(map[uint]context.CancelFunc)
}

// RestartForTenant 重启指定租户的 Stream 客户端（配置变更后使用）
func (m *StreamClientManager) RestartForTenant(ctx context.Context, tenantID uint, eventHandler func(ctx context.Context, corpID, processInstanceID, eventType string) error) error {
	// 先停止
	m.StopForTenant(tenantID)

	// 重新加载租户配置
	tenant, err := m.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return err
	}

	// 重新启动
	return m.StartForTenant(ctx, tenant, eventHandler)
}
