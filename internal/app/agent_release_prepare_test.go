package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestPrepareAgentReleaseConfigAddsSafeShadowDefaultsAndPreservesSecrets(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "prod.yaml")
	backup := filepath.Join(directory, "prod.yaml.pre-agent-p0-123.bak")
	original := []byte("env: prod\nllm:\n  api_key: super-secret\n  protocol_mode: protocol_live\n  model: test-model\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	change, err := PrepareAgentReleaseConfig(path, backup, now)
	if err != nil {
		t.Fatalf("PrepareAgentReleaseConfig() error = %v", err)
	}
	wantKeys := []string{
		"llm.intent_response_format",
		"llm.deterministic_compiler_mode",
		"llm.workflow_store",
		"llm.intent_context_enabled",
		"llm.workflow_migration",
		"llm.workflow_migration_deadline",
		"llm.log_payloads",
	}
	if !change.Changed || !reflect.DeepEqual(change.ChangedKeys, wantKeys) || change.BackupPath != backup {
		t.Fatalf("change = %+v, want keys %v and backup %q", change, wantKeys, backup)
	}
	backupContent, err := os.ReadFile(backup)
	if err != nil || !reflect.DeepEqual(backupContent, original) {
		t.Fatalf("backup content error=%v content=%q", err, backupContent)
	}
	var decoded map[string]any
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(content, &decoded); err != nil {
		t.Fatal(err)
	}
	llm := decoded["llm"].(map[string]any)
	if llm["api_key"] != "super-secret" || llm["intent_response_format"] != "json_object" ||
		llm["deterministic_compiler_mode"] != "observe" || llm["workflow_store"] != "shadow" ||
		llm["intent_context_enabled"] != false || llm["workflow_migration"] != true ||
		llm["workflow_migration_deadline"] != "2026-08-13T12:00:00Z" || llm["log_payloads"] != false {
		t.Fatalf("llm config = %#v", llm)
	}
}

func TestPrepareAgentReleaseConfigDoesNotOverwriteExplicitValues(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "prod.yaml")
	backup := filepath.Join(directory, "backup.yaml")
	original := []byte("llm:\n  intent_response_format: prompt_only\n  deterministic_compiler_mode: fallback\n  workflow_store: database\n  intent_context_enabled: true\n  workflow_migration: false\n  workflow_migration_deadline: 2030-01-01T00:00:00Z\n  log_payloads: false\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	change, err := PrepareAgentReleaseConfig(path, backup, time.Now())
	if err != nil {
		t.Fatalf("PrepareAgentReleaseConfig() error = %v", err)
	}
	if change.Changed {
		t.Fatalf("change = %+v, want unchanged", change)
	}
	content, _ := os.ReadFile(path)
	if !reflect.DeepEqual(content, original) {
		t.Fatalf("config changed: %q", content)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup exists for unchanged config: %v", err)
	}
}

func TestRestoreAgentReleaseConfigRestoresExactBytes(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "prod.yaml")
	backup := filepath.Join(directory, "backup.yaml")
	original := []byte("llm:\n  api_key: secret\n")
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := RestoreAgentReleaseConfig(path, backup); err != nil {
		t.Fatalf("RestoreAgentReleaseConfig() error = %v", err)
	}
	content, _ := os.ReadFile(path)
	if !reflect.DeepEqual(content, original) {
		t.Fatalf("restored content = %q, want %q", content, original)
	}
}
