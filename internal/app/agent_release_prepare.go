package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"schedule_server/scripts/migrations"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

const agentShadowMigrationWindow = 7 * 24 * time.Hour

// AgentReleaseConfigChange describes an auditable production config update.
type AgentReleaseConfigChange struct {
	Changed     bool
	ChangedKeys []string
	BackupPath  string
}

// PrepareAgentReleaseConfig adds only missing Agent P0 rollout settings. It
// never overwrites an explicit non-empty enum or an explicit boolean choice.
func PrepareAgentReleaseConfig(path, backupPath string, now time.Time) (AgentReleaseConfigChange, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return AgentReleaseConfigChange{}, fmt.Errorf("read production config: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return AgentReleaseConfigChange{}, fmt.Errorf("parse production config: %w", err)
	}
	root, err := yamlDocumentMapping(&document)
	if err != nil {
		return AgentReleaseConfigChange{}, err
	}
	llm, found, err := yamlMappingValue(root, "llm")
	if err != nil {
		return AgentReleaseConfigChange{}, err
	}
	if !found || llm.Kind != yaml.MappingNode {
		return AgentReleaseConfigChange{}, errors.New("production config requires an llm mapping")
	}

	changedKeys, err := applyAgentReleaseDefaults(llm, now)
	if err != nil {
		return AgentReleaseConfigChange{}, err
	}
	if len(changedKeys) == 0 {
		return AgentReleaseConfigChange{}, nil
	}

	stat, err := os.Stat(path)
	if err != nil {
		return AgentReleaseConfigChange{}, fmt.Errorf("stat production config: %w", err)
	}
	if err := copyFileExclusive(path, backupPath, stat.Mode()); err != nil {
		return AgentReleaseConfigChange{}, fmt.Errorf("backup production config: %w", err)
	}
	encoded, err := encodeYAMLDocument(&document)
	if err != nil {
		return AgentReleaseConfigChange{}, err
	}
	if writeErr := replaceFile(path, encoded, stat.Mode()); writeErr != nil {
		if restoreErr := RestoreAgentReleaseConfig(path, backupPath); restoreErr != nil {
			return AgentReleaseConfigChange{}, errors.Join(
				fmt.Errorf("write production config: %w", writeErr),
				fmt.Errorf("restore production config: %w", restoreErr),
			)
		}
		return AgentReleaseConfigChange{}, fmt.Errorf("write production config: %w", writeErr)
	}
	return AgentReleaseConfigChange{Changed: true, ChangedKeys: changedKeys, BackupPath: backupPath}, nil
}

func applyAgentReleaseDefaults(llm *yaml.Node, now time.Time) ([]string, error) {
	changedKeys := make([]string, 0, 7)
	stringDefaults := []struct {
		key   string
		value string
	}{
		{key: "intent_response_format", value: "json_object"},
		{key: "deterministic_compiler_mode", value: "observe"},
		{key: "workflow_store", value: agentWorkflowStoreShadow},
	}
	for _, item := range stringDefaults {
		if err := setMissingYAMLString(llm, item.key, item.value, &changedKeys); err != nil {
			return nil, err
		}
	}
	if err := setMissingYAMLBool(llm, "intent_context_enabled", false, &changedKeys); err != nil {
		return nil, err
	}
	if err := setMissingYAMLBool(llm, "workflow_migration", true, &changedKeys); err != nil {
		return nil, err
	}
	if err := setMissingYAMLString(
		llm,
		"workflow_migration_deadline",
		now.UTC().Add(agentShadowMigrationWindow).Format(time.RFC3339),
		&changedKeys,
	); err != nil {
		return nil, err
	}
	if err := setMissingYAMLBool(llm, "log_payloads", false, &changedKeys); err != nil {
		return nil, err
	}
	return changedKeys, nil
}

func setMissingYAMLString(mapping *yaml.Node, key, value string, changedKeys *[]string) error {
	node, exists, err := yamlMappingValue(mapping, key)
	if err != nil {
		return err
	}
	if exists && strings.TrimSpace(node.Value) != "" {
		return nil
	}
	if exists {
		node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!str", value
	} else {
		yamlAppendScalar(mapping, key, "!!str", value)
	}
	*changedKeys = append(*changedKeys, "llm."+key)
	return nil
}

func setMissingYAMLBool(mapping *yaml.Node, key string, value bool, changedKeys *[]string) error {
	_, exists, err := yamlMappingValue(mapping, key)
	if err != nil || exists {
		return err
	}
	yamlAppendScalar(mapping, key, "!!bool", fmt.Sprintf("%t", value))
	*changedKeys = append(*changedKeys, "llm."+key)
	return nil
}

// RestoreAgentReleaseConfig restores the exact pre-change bytes from backup.
func RestoreAgentReleaseConfig(path, backupPath string) error {
	content, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read production config backup: %w", err)
	}
	stat, err := os.Stat(backupPath)
	if err != nil {
		return fmt.Errorf("stat production config backup: %w", err)
	}
	if err := replaceFile(path, content, stat.Mode()); err != nil {
		return fmt.Errorf("restore production config backup: %w", err)
	}
	return nil
}

// ApplyAgentP0Migrations executes the reviewed idempotent DDL files.
func ApplyAgentP0Migrations(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return errors.New("agent migration database is nil")
	}
	for _, migration := range migrations.AgentP0() {
		if err := db.WithContext(ctx).Exec(migration.SQL).Error; err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.Name, err)
		}
	}
	return nil
}

func yamlDocumentMapping(document *yaml.Node) (*yaml.Node, error) {
	if document == nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("production config root must be a YAML mapping")
	}
	return document.Content[0], nil
}

func yamlMappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool, error) {
	var result *yaml.Node
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value != key {
			continue
		}
		if result != nil {
			return nil, false, fmt.Errorf("production config contains duplicate key %s", key)
		}
		result = mapping.Content[index+1]
	}
	return result, result != nil, nil
}

func yamlAppendScalar(mapping *yaml.Node, key, tag, value string) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value},
	)
}

func encodeYAMLDocument(document *yaml.Node) ([]byte, error) {
	var builder strings.Builder
	encoder := yaml.NewEncoder(&builder)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encode production config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close production config encoder: %w", err)
	}
	return []byte(builder.String()), nil
}

func copyFileExclusive(source, target string, mode os.FileMode) (err error) {
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	targetFile, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := targetFile.Close(); err == nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(targetFile, sourceFile)
	return err
}

func replaceFile(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".agent-release-config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}

	oldFile, err := os.CreateTemp(filepath.Dir(path), ".agent-release-old-*")
	if err != nil {
		return err
	}
	oldPath := oldFile.Name()
	if err := oldFile.Close(); err != nil {
		return err
	}
	if err := os.Remove(oldPath); err != nil {
		return err
	}
	if err := os.Rename(path, oldPath); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if restoreErr := os.Rename(oldPath, path); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore replaced file: %w", restoreErr))
		}
		return err
	}
	if err := os.Remove(oldPath); err != nil {
		return err
	}
	return nil
}
