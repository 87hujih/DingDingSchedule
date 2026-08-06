package inits

import (
	"reflect"
	"testing"

	"schedule_server/internal/model"
)

func TestAutoMigrateModelsKeepsAgentWorkflowOutOfProduction(t *testing.T) {
	t.Parallel()

	if containsModel(autoMigrateModels("prod"), reflect.TypeOf(&model.AgentWorkflow{})) {
		t.Fatal("production AutoMigrate unexpectedly contains AgentWorkflow; production DDL must be reviewed separately")
	}
	if !containsModel(autoMigrateModels("dev"), reflect.TypeOf(&model.AgentWorkflow{})) {
		t.Fatal("development AutoMigrate does not contain AgentWorkflow")
	}
	if containsModel(autoMigrateModels("prod"), reflect.TypeOf(&model.AgentWriteLedger{})) {
		t.Fatal("production AutoMigrate unexpectedly contains AgentWriteLedger; production DDL must be reviewed separately")
	}
	if !containsModel(autoMigrateModels("dev"), reflect.TypeOf(&model.AgentWriteLedger{})) {
		t.Fatal("development AutoMigrate does not contain AgentWriteLedger")
	}
}

func containsModel(models []any, target reflect.Type) bool {
	for _, candidate := range models {
		if reflect.TypeOf(candidate) == target {
			return true
		}
	}
	return false
}
