package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"schedule_server/internal/model"
	"schedule_server/pkg/dingtalk"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAgentReadinessRoutesExposeOnlyNonSensitiveRuntimeState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	state := newAgentReadinessState()
	cfg, err := ParseAgentRuntimeConfig(validRuntimeLLMConfig(), "dev")
	if err != nil {
		t.Fatalf("ParseAgentRuntimeConfig() error = %v", err)
	}
	state.configure(cfg)
	router := gin.New()
	registerReadinessRoutes(router, state)

	public := httptest.NewRecorder()
	router.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if public.Code != http.StatusOK || strings.TrimSpace(public.Body.String()) != `{"ready":true}` {
		t.Fatalf("public readiness = %d %s", public.Code, public.Body.String())
	}

	internal := httptest.NewRecorder()
	router.ServeHTTP(internal, httptest.NewRequest(http.MethodGet, "/internal/readiness", nil))
	if internal.Code != http.StatusOK {
		t.Fatalf("internal readiness status = %d", internal.Code)
	}
	var payload struct {
		Ready bool                   `json:"ready"`
		Agent AgentReadinessSnapshot `json:"agent"`
	}
	if err := json.Unmarshal(internal.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if !payload.Ready || !payload.Agent.Ready ||
		payload.Agent.WorkflowStore != "memory" ||
		payload.Agent.ConfigFingerprint != cfg.Fingerprint() {
		t.Fatalf("readiness payload = %+v", payload)
	}
	for _, secret := range []string{"test-credential", "llm.example.test"} {
		if strings.Contains(internal.Body.String(), secret) {
			t.Fatalf("readiness leaked sensitive value %q: %s", secret, internal.Body.String())
		}
	}

	state.markWorkflowStoreUnavailable()
	unavailable := httptest.NewRecorder()
	router.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if unavailable.Code != http.StatusServiceUnavailable ||
		strings.TrimSpace(unavailable.Body.String()) != `{"ready":false}` {
		t.Fatalf("unavailable readiness = %d %s", unavailable.Code, unavailable.Body.String())
	}
}

func TestAgentReadinessGateRejectsMessagesWhileStoreUnavailable(t *testing.T) {
	t.Parallel()

	state := newAgentReadinessState()
	cfg, err := ParseAgentRuntimeConfig(validRuntimeLLMConfig(), "dev")
	if err != nil {
		t.Fatalf("ParseAgentRuntimeConfig() error = %v", err)
	}
	state.configure(cfg)
	var calls atomic.Int32
	handler := state.wrapChat(func(context.Context, *dingtalk.ChatMessage) (string, error) {
		calls.Add(1)
		return "ok", nil
	})

	state.markWorkflowStoreUnavailable()
	reply, err := handler(context.Background(), &dingtalk.ChatMessage{Content: "hello"})
	if err != nil {
		t.Fatalf("gated handler error = %v", err)
	}
	if reply != agentUnavailableReply || calls.Load() != 0 {
		t.Fatalf("gated reply/calls = %q/%d", reply, calls.Load())
	}

	state.markWorkflowStoreReady()
	reply, err = handler(context.Background(), &dingtalk.ChatMessage{Content: "hello"})
	if err != nil || reply != "ok" || calls.Load() != 1 {
		t.Fatalf("ready reply/error/calls = %q/%v/%d", reply, err, calls.Load())
	}
}

func TestProbeAgentWorkflowDatabaseRequiresMigrations(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := probeAgentWorkflowDatabase(context.Background(), db); err == nil {
		t.Fatal("probe without migrations error = nil")
	}
	if err := db.AutoMigrate(&model.AgentWorkflow{}, &model.AgentWriteLedger{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := probeAgentWorkflowDatabase(context.Background(), db); err != nil {
		t.Fatalf("probe after migrations error = %v", err)
	}
}
