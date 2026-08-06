package migrations

import (
	"strings"
	"testing"
)

func TestAgentP0EmbedsReviewedIdempotentDDL(t *testing.T) {
	t.Parallel()

	migrations := AgentP0()
	if len(migrations) != 2 {
		t.Fatalf("AgentP0() length = %d, want 2", len(migrations))
	}
	wantTables := []string{"agent_workflows", "agent_write_ledgers"}
	for index, migration := range migrations {
		if strings.TrimSpace(migration.Name) == "" ||
			!strings.Contains(migration.SQL, "CREATE TABLE IF NOT EXISTS") ||
			!strings.Contains(migration.SQL, wantTables[index]) {
			t.Fatalf("migration[%d] = %+v, want reviewed idempotent DDL for %s", index, migration, wantTables[index])
		}
	}
}
