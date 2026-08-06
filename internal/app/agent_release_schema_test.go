package app

import (
	"slices"
	"strings"
	"testing"
)

func TestLegacyAgentWorkflowMigrationPlanPreservesOldWriterCompatibility(t *testing.T) {
	t.Parallel()

	columns := map[string]bool{
		"state":         true,
		"workflow_type": true,
		"version":       true,
		"snapshot_json": true,
		"expires_at":    true,
	}
	steps := legacyAgentWorkflowMigrationPlan(
		func(name string) bool { return columns[name] },
		func(string) bool { return false },
	)
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.name)
	}
	for _, required := range []string{
		"add_workflow_state",
		"backfill_workflow_state",
		"make_legacy_state_nullable",
		"add_snapshot_schema_version",
		"add_execution_status",
		"add_lease_expires_at",
		"add_recovery_index",
	} {
		if !slices.Contains(names, required) {
			t.Fatalf("migration names = %v, missing %s", names, required)
		}
	}
	joined := strings.Join(migrationSQL(steps), "\n")
	if !strings.Contains(joined, "workflow_state = state") ||
		!strings.Contains(joined, "MODIFY COLUMN state VARCHAR(32) NULL") {
		t.Fatalf("legacy compatibility SQL = %s", joined)
	}
}

func TestLegacyAgentWorkflowMigrationPlanIsEmptyForCurrentSchema(t *testing.T) {
	t.Parallel()

	columns := map[string]bool{
		"workflow_state":                   true,
		"snapshot_schema_version":          true,
		"execution_status":                 true,
		"execution_token":                  true,
		"execution_operation":              true,
		"business_key":                     true,
		"request_id":                       true,
		"execution_request_schema_version": true,
		"execution_request_json":           true,
		"execution_result_schema_version":  true,
		"execution_result_json":            true,
		"write_effect":                     true,
		"lease_expires_at":                 true,
	}
	steps := legacyAgentWorkflowMigrationPlan(
		func(name string) bool { return columns[name] },
		func(name string) bool {
			return name == "idx_agent_workflow_expiry" || name == "idx_agent_workflow_recovery"
		},
	)
	if len(steps) != 0 {
		t.Fatalf("migration plan = %+v, want empty", steps)
	}
}

func migrationSQL(steps []agentSchemaMigrationStep) []string {
	result := make([]string, 0, len(steps))
	for _, step := range steps {
		result = append(result, step.sql)
	}
	return result
}
