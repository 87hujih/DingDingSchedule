package migrations

import _ "embed"

// AgentMigration is one reviewed, idempotent Agent production DDL migration.
type AgentMigration struct {
	Name string
	SQL  string
}

var (
	//go:embed 20260730_create_agent_workflows.sql
	agentWorkflowsSQL string

	//go:embed 20260730_create_agent_write_ledgers.sql
	agentWriteLedgersSQL string
)

// AgentP0 returns the reviewed Agent P0 DDL in dependency order.
func AgentP0() []AgentMigration {
	return []AgentMigration{
		{Name: "20260730_create_agent_workflows", SQL: agentWorkflowsSQL},
		{Name: "20260730_create_agent_write_ledgers", SQL: agentWriteLedgersSQL},
	}
}
