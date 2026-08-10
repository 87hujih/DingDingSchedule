package app

import (
	"strings"
	"testing"
	"time"

	"schedule_server/config"
)

func TestParseAgentRuntimeConfigDefaultsCompilerTimeoutToTwelveSeconds(t *testing.T) {
	t.Parallel()

	got, err := ParseAgentRuntimeConfig(validRuntimeLLMConfig(), "dev")
	if err != nil {
		t.Fatalf("ParseAgentRuntimeConfig() error = %v", err)
	}
	if got.IntentCompilerTimeout != 12*time.Second {
		t.Fatalf("timeout = %s, want 12s", got.IntentCompilerTimeout)
	}
}

func TestParseAgentRuntimeConfigRejectsLiveSemanticTimeoutBelowEightSeconds(t *testing.T) {
	t.Parallel()

	cfg := validRuntimeLLMConfig()
	cfg.IntentCompilerTimeout = "5s"
	if _, err := ParseAgentRuntimeConfig(cfg, "dev"); err == nil || !strings.Contains(err.Error(), "at least 8s") {
		t.Fatalf("error = %v, want semantic timeout floor", err)
	}
}

func TestParseAgentRuntimeConfigRejectsInvalidTimeout(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"bad", "0s", "999ms", "16s", "-1s"} {
		value := value
		t.Run(value, func(t *testing.T) {
			cfg := validRuntimeLLMConfig()
			cfg.IntentCompilerTimeout = value
			if _, err := ParseAgentRuntimeConfig(cfg, "dev"); err == nil {
				t.Fatalf("timeout %q accepted, want error", value)
			}
		})
	}
}

func TestParseAgentRuntimeConfigRejectsInvalidEnums(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*config.LLM)
	}{
		{"protocol", func(cfg *config.LLM) { cfg.ProtocolMode = "typo" }},
		{"format", func(cfg *config.LLM) { cfg.IntentResponseFormat = "markdown" }},
		{"compiler", func(cfg *config.LLM) { cfg.DeterministicCompilerMode = "magic" }},
		{"store", func(cfg *config.LLM) { cfg.WorkflowStore = "fallback" }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := validRuntimeLLMConfig()
			tt.mutate(&cfg)
			if _, err := ParseAgentRuntimeConfig(cfg, "dev"); err == nil {
				t.Fatal("ParseAgentRuntimeConfig() error = nil")
			}
		})
	}
}

func TestParseAgentRuntimeConfigAcceptsProtocolShadow(t *testing.T) {
	t.Parallel()

	cfg := validRuntimeLLMConfig()
	cfg.ProtocolMode = "protocol_shadow"
	if _, err := ParseAgentRuntimeConfig(cfg, "dev"); err != nil {
		t.Fatalf("protocol_shadow rejected: %v", err)
	}
}

func TestProductionLiveRejectsMemoryWorkflowStore(t *testing.T) {
	t.Parallel()

	_, err := ParseAgentRuntimeConfig(validRuntimeLLMConfig(), "prod")
	if err == nil || !strings.Contains(err.Error(), "memory") {
		t.Fatalf("error = %v, want production memory rejection", err)
	}
}

func TestProductionShadowRequiresMigrationDeadline(t *testing.T) {
	t.Parallel()

	cfg := validRuntimeLLMConfig()
	cfg.WorkflowStore = "shadow"
	if _, err := ParseAgentRuntimeConfig(cfg, "prod"); err == nil {
		t.Fatal("shadow without migration flag accepted")
	}

	cfg.WorkflowMigration = true
	if _, err := ParseAgentRuntimeConfig(cfg, "prod"); err == nil {
		t.Fatal("shadow without deadline accepted")
	}

	cfg.WorkflowMigrationDeadline = "2099-01-01T00:00:00Z"
	if _, err := ParseAgentRuntimeConfig(cfg, "prod"); err != nil {
		t.Fatalf("valid shadow migration rejected: %v", err)
	}
}

func TestProtocolLiveRequiresEndpointModelAndCredential(t *testing.T) {
	t.Parallel()

	tests := []func(*config.LLM){
		func(cfg *config.LLM) { cfg.BaseURL = "" },
		func(cfg *config.LLM) { cfg.Model = "" },
		func(cfg *config.LLM) { cfg.APIKey = "" },
	}
	for _, mutate := range tests {
		cfg := validRuntimeLLMConfig()
		mutate(&cfg)
		if _, err := ParseAgentRuntimeConfig(cfg, "dev"); err == nil {
			t.Fatal("invalid live LLM config accepted")
		}
	}
}

func TestAgentConfigFingerprintIsStableAndExcludesSecrets(t *testing.T) {
	t.Parallel()

	first := validRuntimeLLMConfig()
	first.APIKey = "first-secret"
	first.BaseURL = "https://first.example.test/v1"
	first.RouterAPIKey = "first-router-secret"
	first.RouterBaseURL = "https://first-router.example.test/v1"
	firstRuntime, err := ParseAgentRuntimeConfig(first, "dev")
	if err != nil {
		t.Fatalf("ParseAgentRuntimeConfig(first) error = %v", err)
	}

	second := first
	second.APIKey = "second-secret"
	second.BaseURL = "https://second.example.test/v1"
	second.RouterAPIKey = "second-router-secret"
	second.RouterBaseURL = "https://second-router.example.test/v1"
	secondRuntime, err := ParseAgentRuntimeConfig(second, "dev")
	if err != nil {
		t.Fatalf("ParseAgentRuntimeConfig(second) error = %v", err)
	}
	if firstRuntime.Fingerprint() != secondRuntime.Fingerprint() {
		t.Fatalf("fingerprints differ after credential/endpoint change: %q != %q", firstRuntime.Fingerprint(), secondRuntime.Fingerprint())
	}
	if len(firstRuntime.Fingerprint()) != agentConfigFingerprintLength {
		t.Fatalf("fingerprint length = %d, want %d", len(firstRuntime.Fingerprint()), agentConfigFingerprintLength)
	}

	second.Model = "changed-model"
	changedRuntime, err := ParseAgentRuntimeConfig(second, "dev")
	if err != nil {
		t.Fatalf("ParseAgentRuntimeConfig(changed) error = %v", err)
	}
	if changedRuntime.Fingerprint() == firstRuntime.Fingerprint() {
		t.Fatal("fingerprint did not change after non-sensitive runtime setting changed")
	}
}

func TestDatabaseWorkflowStoreStartsRecoveryRuntimeWithoutStreamMode(t *testing.T) {
	t.Parallel()

	cfg, err := ParseAgentRuntimeConfig(validRuntimeLLMConfig(), "dev")
	if err != nil {
		t.Fatalf("ParseAgentRuntimeConfig() error = %v", err)
	}
	if agentRuntimeMustStart(false, cfg) {
		t.Fatal("memory workflow store unexpectedly requires Agent runtime without stream")
	}
	cfg.WorkflowStore = "database"
	if !agentRuntimeMustStart(false, cfg) {
		t.Fatal("database workflow store did not require recovery runtime without stream")
	}
}

func validRuntimeLLMConfig() config.LLM {
	return config.LLM{
		BaseURL:                   "https://llm.example.test/v1/chat/completions",
		APIKey:                    "test-credential",
		Model:                     "test-model",
		ProtocolMode:              "protocol_live",
		IntentResponseFormat:      "json_object",
		DeterministicCompilerMode: "short_circuit",
		WorkflowStore:             "memory",
	}
}
