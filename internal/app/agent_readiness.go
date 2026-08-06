package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"schedule_server/internal/model"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/dingtalk"

	"gorm.io/gorm"
)

const (
	agentReadinessReady       = "ready"
	agentReadinessDegraded    = "degraded"
	agentReadinessNotReady    = "not_ready"
	agentReadinessUnavailable = "workflow_store_unavailable"

	agentUnavailableReply = "智能助手暂时不可用，请稍后重试"
)

// AgentReadinessSnapshot is the non-sensitive Agent state exposed to internal
// readiness consumers.
type AgentReadinessSnapshot struct {
	Ready             bool   `json:"ready"`
	Status            string `json:"status"`
	ProtocolMode      string `json:"protocol_mode"`
	WorkflowStore     string `json:"workflow_store"`
	ConfigFingerprint string `json:"config_fingerprint"`
	ReasonCode        string `json:"reason_code,omitempty"`
}

type agentReadinessState struct {
	mu       sync.RWMutex
	snapshot AgentReadinessSnapshot
}

var runtimeAgentReadiness = newAgentReadinessState()

func newAgentReadinessState() *agentReadinessState {
	return &agentReadinessState{
		snapshot: AgentReadinessSnapshot{
			Ready:  false,
			Status: agentReadinessNotReady,
		},
	}
}

func (s *agentReadinessState) configure(cfg AgentRuntimeConfig) {
	status := agentReadinessReady
	if cfg.WorkflowStore == agentWorkflowStoreShadow {
		status = agentReadinessDegraded
	}
	s.mu.Lock()
	s.snapshot = AgentReadinessSnapshot{
		Ready:             true,
		Status:            status,
		ProtocolMode:      cfg.ProtocolMode,
		WorkflowStore:     cfg.WorkflowStore,
		ConfigFingerprint: cfg.Fingerprint(),
	}
	s.mu.Unlock()
}

func (s *agentReadinessState) markWorkflowStoreUnavailable() {
	s.mu.Lock()
	s.snapshot.Ready = false
	s.snapshot.Status = agentReadinessNotReady
	s.snapshot.ReasonCode = agentReadinessUnavailable
	s.mu.Unlock()
}

func (s *agentReadinessState) markWorkflowStoreReady() {
	s.mu.Lock()
	s.snapshot.Ready = true
	s.snapshot.Status = agentReadinessReady
	s.snapshot.ReasonCode = ""
	s.mu.Unlock()
}

func (s *agentReadinessState) get() AgentReadinessSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *agentReadinessState) wrapChat(
	next func(context.Context, *dingtalk.ChatMessage) (string, error),
) func(context.Context, *dingtalk.ChatMessage) (string, error) {
	return func(ctx context.Context, msg *dingtalk.ChatMessage) (string, error) {
		if !s.get().Ready {
			return agentUnavailableReply, nil
		}
		if next == nil {
			return "", errors.New("agent chat handler is nil")
		}
		return next(ctx, msg)
	}
}

func probeAgentWorkflowDatabase(ctx context.Context, db *gorm.DB) error {
	if err := probeAgentWorkflowDatabaseAccess(ctx, db); err != nil {
		return err
	}
	scopedDB := db.WithContext(ctx)
	migrator := scopedDB.Migrator()
	requiredSchema := []struct {
		name    string
		model   any
		columns []string
	}{
		{
			name:  "agent_workflows",
			model: &model.AgentWorkflow{},
			columns: []string{
				"version",
				"execution_status",
				"execution_token",
				"execution_request_json",
				"execution_result_json",
			},
		},
		{
			name:    "agent_write_ledgers",
			model:   &model.AgentWriteLedger{},
			columns: []string{"tenant_id", "business_key", "operation", "write_effect"},
		},
	}
	for _, requirement := range requiredSchema {
		if !migrator.HasTable(requirement.model) {
			return fmt.Errorf("required agent workflow table %s is missing", requirement.name)
		}
		for _, column := range requirement.columns {
			if !migrator.HasColumn(requirement.model, column) {
				return fmt.Errorf("required agent workflow column %s.%s is missing", requirement.name, column)
			}
		}
	}
	return nil
}

// CheckAgentWorkflowDatabase verifies that the Agent workflow database is
// reachable, has the reviewed schema, and grants the runtime read/write access.
// It is exported for the release preflight command so deployment can fail
// before replacing the currently running container.
func CheckAgentWorkflowDatabase(ctx context.Context, db *gorm.DB) error {
	return probeAgentWorkflowDatabase(ctx, db)
}

func probeAgentWorkflowDatabaseAccess(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return errors.New("agent workflow database is nil")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return err
	}
	scopedDB := db.WithContext(ctx)
	var workflowProbe int
	if err := scopedDB.WithContext(tenantctx.WithSkipTenantScope(ctx)).
		Raw("SELECT 1 FROM agent_workflows LIMIT 1").
		Scan(&workflowProbe).Error; err != nil {
		return err
	}
	var ledgerProbe int
	if err := scopedDB.WithContext(tenantctx.WithSkipTenantScope(ctx)).
		Raw("SELECT 1 FROM agent_write_ledgers LIMIT 1").
		Scan(&ledgerProbe).Error; err != nil {
		return err
	}
	if err := scopedDB.Exec(
		"UPDATE agent_workflows SET version = version WHERE 1 = 0",
	).Error; err != nil {
		return err
	}
	if err := scopedDB.Exec(
		"UPDATE agent_write_ledgers SET updated_at = updated_at WHERE 1 = 0",
	).Error; err != nil {
		return err
	}
	return nil
}

func monitorAgentWorkflowDatabase(
	ctx context.Context,
	db *gorm.DB,
	state *agentReadinessState,
	interval time.Duration,
) {
	if state == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := probeAgentWorkflowDatabaseAccess(probeCtx, db)
			cancel()
			if err != nil {
				state.markWorkflowStoreUnavailable()
				continue
			}
			state.markWorkflowStoreReady()
		}
	}
}
