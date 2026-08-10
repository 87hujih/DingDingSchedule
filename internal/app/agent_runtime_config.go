package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"schedule_server/config"
)

const (
	defaultIntentCompilerTimeout = 12 * time.Second
	minIntentCompilerTimeout     = time.Second
	minSemanticIntentTimeout     = 8 * time.Second
	maxIntentCompilerTimeout     = 15 * time.Second
	agentWorkflowStoreMemory     = "memory"
	agentWorkflowStoreShadow     = "shadow"
	agentWorkflowStoreDatabase   = "database"
	agentConfigFingerprintLength = 12
)

type AgentRuntimeConfig struct {
	ProtocolMode              string
	Model                     string
	IntentCompilerTimeout     time.Duration
	IntentResponseFormat      string
	DeterministicCompilerMode string
	IntentContextEnabled      bool
	WorkflowStore             string
	WorkflowMigration         bool
	WorkflowMigrationDeadline time.Time
	LogPayloads               bool
}

// UsesWorkflowDatabase reports whether the configured workflow store depends
// on the database either as the primary store or as a shadow mirror.
func (c AgentRuntimeConfig) UsesWorkflowDatabase() bool {
	return c.WorkflowStore == agentWorkflowStoreShadow || c.WorkflowStore == agentWorkflowStoreDatabase
}

func ParseAgentRuntimeConfig(cfg config.LLM, env string) (AgentRuntimeConfig, error) { //nolint:gocyclo // Startup fail-fast validation keeps all coupled Agent settings in one audit point.
	result := AgentRuntimeConfig{
		ProtocolMode:              strings.TrimSpace(cfg.ProtocolMode),
		Model:                     strings.TrimSpace(cfg.Model),
		IntentResponseFormat:      strings.TrimSpace(cfg.IntentResponseFormat),
		DeterministicCompilerMode: strings.TrimSpace(cfg.DeterministicCompilerMode),
		IntentContextEnabled:      cfg.IntentContextEnabled,
		WorkflowStore:             strings.TrimSpace(cfg.WorkflowStore),
		WorkflowMigration:         cfg.WorkflowMigration,
		LogPayloads:               cfg.LogPayloads,
	}

	if err := validateEnum("protocol_mode", result.ProtocolMode, "legacy", "protocol_shadow", "protocol_live"); err != nil {
		return AgentRuntimeConfig{}, err
	}
	if err := validateEnum("intent_response_format", result.IntentResponseFormat, "json_object", "prompt_only"); err != nil {
		return AgentRuntimeConfig{}, err
	}
	if err := validateEnum("deterministic_compiler_mode", result.DeterministicCompilerMode, "observe", "fallback", "short_circuit"); err != nil {
		return AgentRuntimeConfig{}, err
	}
	if err := validateEnum(
		"workflow_store",
		result.WorkflowStore,
		agentWorkflowStoreMemory,
		agentWorkflowStoreShadow,
		agentWorkflowStoreDatabase,
	); err != nil {
		return AgentRuntimeConfig{}, err
	}

	timeout, err := parseIntentCompilerTimeout(cfg.IntentCompilerTimeout)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	result.IntentCompilerTimeout = timeout

	if result.ProtocolMode == "protocol_live" {
		if result.IntentCompilerTimeout < minSemanticIntentTimeout {
			return AgentRuntimeConfig{}, fmt.Errorf(
				"protocol_live intent_compiler_timeout must be at least %s for semantic compilation",
				minSemanticIntentTimeout,
			)
		}
		if err := validateLiveLLMConfig(cfg); err != nil {
			return AgentRuntimeConfig{}, err
		}
	}

	production := strings.EqualFold(strings.TrimSpace(env), "prod") ||
		strings.EqualFold(strings.TrimSpace(env), "production")
	if production && result.ProtocolMode == "protocol_live" && result.WorkflowStore == agentWorkflowStoreMemory {
		return AgentRuntimeConfig{}, errors.New("production protocol_live forbids memory workflow_store")
	}
	if production && result.WorkflowStore == agentWorkflowStoreShadow {
		if !result.WorkflowMigration {
			return AgentRuntimeConfig{}, errors.New("production shadow workflow_store requires workflow_migration=true")
		}
		deadline, err := time.Parse(time.RFC3339, strings.TrimSpace(cfg.WorkflowMigrationDeadline))
		if err != nil {
			return AgentRuntimeConfig{}, fmt.Errorf("invalid workflow_migration_deadline: %w", err)
		}
		if !deadline.After(time.Now()) {
			return AgentRuntimeConfig{}, errors.New("workflow_migration_deadline must be in the future")
		}
		result.WorkflowMigrationDeadline = deadline
	}
	if production && result.LogPayloads {
		return AgentRuntimeConfig{}, errors.New("production forbids llm.log_payloads")
	}
	return result, nil
}

// Fingerprint returns a stable prefix derived only from non-sensitive Agent
// runtime settings. Credentials and endpoints are deliberately absent from
// AgentRuntimeConfig so they cannot accidentally enter logs or readiness.
func (c AgentRuntimeConfig) Fingerprint() string {
	payload := struct {
		ProtocolMode              string `json:"protocol_mode"`
		Model                     string `json:"model"`
		IntentCompilerTimeoutMS   int64  `json:"intent_compiler_timeout_ms"`
		IntentResponseFormat      string `json:"intent_response_format"`
		DeterministicCompilerMode string `json:"deterministic_compiler_mode"`
		IntentContextEnabled      bool   `json:"intent_context_enabled"`
		WorkflowStore             string `json:"workflow_store"`
		WorkflowMigration         bool   `json:"workflow_migration"`
		WorkflowMigrationDeadline string `json:"workflow_migration_deadline,omitempty"`
		LogPayloads               bool   `json:"log_payloads"`
	}{
		ProtocolMode:              c.ProtocolMode,
		Model:                     c.Model,
		IntentCompilerTimeoutMS:   c.IntentCompilerTimeout.Milliseconds(),
		IntentResponseFormat:      c.IntentResponseFormat,
		DeterministicCompilerMode: c.DeterministicCompilerMode,
		IntentContextEnabled:      c.IntentContextEnabled,
		WorkflowStore:             c.WorkflowStore,
		WorkflowMigration:         c.WorkflowMigration,
		LogPayloads:               c.LogPayloads,
	}
	if !c.WorkflowMigrationDeadline.IsZero() {
		payload.WorkflowMigrationDeadline = c.WorkflowMigrationDeadline.UTC().Format(time.RFC3339)
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		panic("marshal agent runtime fingerprint: " + err.Error())
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])[:agentConfigFingerprintLength]
}

func parseIntentCompilerTimeout(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultIntentCompilerTimeout, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid intent_compiler_timeout: %w", err)
	}
	if timeout < minIntentCompilerTimeout || timeout > maxIntentCompilerTimeout {
		return 0, fmt.Errorf(
			"intent_compiler_timeout must be between %s and %s",
			minIntentCompilerTimeout,
			maxIntentCompilerTimeout,
		)
	}
	return timeout, nil
}

func validateEnum(name, value string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("invalid %s %q", name, value)
}

func validateLiveLLMConfig(cfg config.LLM) error {
	endpoint, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return errors.New("protocol_live requires a valid http(s) llm.base_url")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return errors.New("protocol_live requires llm.model")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("protocol_live requires llm.api_key")
	}
	return nil
}
