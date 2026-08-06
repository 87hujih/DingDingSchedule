package app

import (
	"encoding/json"
	"strings"
	"testing"

	"schedule_server/global"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestAgentRuntimeStartupLogExcludesCredentials(t *testing.T) {
	previousConfig := global.AppConfig
	previousLog := global.Log
	t.Cleanup(func() {
		global.AppConfig = previousConfig
		global.Log = previousLog
	})

	cfg := validRuntimeLLMConfig()
	cfg.APIKey = "startup-log-secret"
	cfg.BaseURL = "https://user:password@example.test/v1?token=query-secret"
	runtimeCfg, err := ParseAgentRuntimeConfig(cfg, "dev")
	if err != nil {
		t.Fatalf("ParseAgentRuntimeConfig() error = %v", err)
	}
	global.AppConfig.Env = "dev"
	core, logs := observer.New(zap.InfoLevel)
	global.Log = zap.New(core).Sugar()

	logAgentRuntimeConfig(runtimeCfg)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("startup log entries = %d, want 1", len(entries))
	}
	payload, err := json.Marshal(entries[0].ContextMap())
	if err != nil {
		t.Fatalf("marshal log context: %v", err)
	}
	for _, secret := range []string{"startup-log-secret", "password", "query-secret"} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("startup log leaked %q: %s", secret, payload)
		}
	}
	if entries[0].ContextMap()["fingerprint"] != runtimeCfg.Fingerprint() {
		t.Fatalf("fingerprint = %v, want %s", entries[0].ContextMap()["fingerprint"], runtimeCfg.Fingerprint())
	}
}
