package app

import (
	"context"
	"fmt"

	"schedule_server/internal/model"

	"gorm.io/gorm"
)

type agentSchemaMigrationStep struct {
	name string
	sql  string
}

// UpgradeLegacyAgentWorkflowSchema expands the legacy production table in
// place. The legacy state column remains nullable during shadow rollout so the
// old container can keep serving until the new image passes every preflight.
func UpgradeLegacyAgentWorkflowSchema(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("legacy agent workflow migration database is nil")
	}
	migrator := db.WithContext(ctx).Migrator()
	hasColumn := func(name string) bool {
		return migrator.HasColumn(&model.AgentWorkflow{}, name)
	}
	hasIndex := func(name string) bool {
		return migrator.HasIndex(&model.AgentWorkflow{}, name)
	}
	for _, step := range legacyAgentWorkflowMigrationPlan(hasColumn, hasIndex) {
		if err := db.WithContext(ctx).Exec(step.sql).Error; err != nil {
			return fmt.Errorf("apply legacy agent workflow migration %s: %w", step.name, err)
		}
	}
	return nil
}

func legacyAgentWorkflowMigrationPlan(
	hasColumn func(string) bool,
	hasIndex func(string) bool,
) []agentSchemaMigrationStep {
	steps := make([]agentSchemaMigrationStep, 0, 16)
	legacyState := hasColumn("state")
	if !hasColumn("workflow_state") {
		steps = append(steps, agentSchemaMigrationStep{
			name: "add_workflow_state",
			sql:  "ALTER TABLE agent_workflows ADD COLUMN workflow_state VARCHAR(32) NOT NULL DEFAULT '' AFTER workflow_type",
		})
	}
	if legacyState {
		steps = append(steps,
			agentSchemaMigrationStep{
				name: "backfill_workflow_state",
				sql:  "UPDATE agent_workflows SET workflow_state = state WHERE workflow_state = ''",
			},
			agentSchemaMigrationStep{
				name: "make_legacy_state_nullable",
				sql:  "ALTER TABLE agent_workflows MODIFY COLUMN state VARCHAR(32) NULL",
			},
		)
	}
	columns := []struct {
		name       string
		definition string
	}{
		{name: "snapshot_schema_version", definition: "SMALLINT UNSIGNED NOT NULL DEFAULT 1 AFTER version"},
		{name: "execution_status", definition: "VARCHAR(32) NOT NULL DEFAULT 'idle' AFTER snapshot_json"},
		{name: "execution_token", definition: "VARCHAR(64) NULL AFTER execution_status"},
		{name: "execution_operation", definition: "VARCHAR(64) NULL AFTER execution_token"},
		{name: "business_key", definition: "CHAR(64) NULL AFTER execution_operation"},
		{name: "request_id", definition: "VARCHAR(64) NULL AFTER business_key"},
		{name: "execution_request_schema_version", definition: "SMALLINT UNSIGNED NULL AFTER request_id"},
		{name: "execution_request_json", definition: "LONGTEXT NULL AFTER execution_request_schema_version"},
		{name: "execution_result_schema_version", definition: "SMALLINT UNSIGNED NULL AFTER execution_request_json"},
		{name: "execution_result_json", definition: "LONGTEXT NULL AFTER execution_result_schema_version"},
		{name: "write_effect", definition: "VARCHAR(32) NULL AFTER execution_result_json"},
		{name: "lease_expires_at", definition: "DATETIME(3) NULL AFTER write_effect"},
	}
	for _, column := range columns {
		if hasColumn(column.name) {
			continue
		}
		steps = append(steps, agentSchemaMigrationStep{
			name: "add_" + column.name,
			sql:  "ALTER TABLE agent_workflows ADD COLUMN " + column.name + " " + column.definition,
		})
	}
	if !hasIndex("idx_agent_workflow_expiry") {
		steps = append(steps, agentSchemaMigrationStep{
			name: "add_expiry_index",
			sql:  "ALTER TABLE agent_workflows ADD INDEX idx_agent_workflow_expiry (expires_at)",
		})
	}
	if !hasIndex("idx_agent_workflow_recovery") {
		steps = append(steps, agentSchemaMigrationStep{
			name: "add_recovery_index",
			sql:  "ALTER TABLE agent_workflows ADD INDEX idx_agent_workflow_recovery (execution_status, lease_expires_at)",
		})
	}
	return steps
}
