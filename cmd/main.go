package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"schedule_server/global"
	"schedule_server/inits"
	"schedule_server/internal/app"
)

func main() {
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "agent-config-check":
			if err := runAgentConfigCheck(); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "agent config invalid: %v\n", err)
				os.Exit(1)
			}
			return
		case "agent-release-check":
			if err := runAgentReleaseCheck(); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "agent release preflight failed: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}
	inits.Init()
	if err := app.RunServer(); err != nil {
		global.Log.Fatalw("服务启动失败", "error", err)
	}
}

func runAgentReleaseCheck() (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("initialize release preflight: %v", recovered)
		}
	}()

	inits.ConfigInit()
	cfg, err := app.ParseAgentRuntimeConfig(global.AppConfig.LLM, global.AppConfig.Env)
	if err != nil {
		return err
	}
	if !cfg.UsesWorkflowDatabase() {
		return json.NewEncoder(os.Stdout).Encode(struct {
			WorkflowStore     string `json:"workflow_store"`
			DatabaseRequired  bool   `json:"database_required"`
			DatabaseReady     bool   `json:"database_ready"`
			ConfigFingerprint string `json:"config_fingerprint"`
		}{cfg.WorkflowStore, false, true, cfg.Fingerprint()})
	}

	inits.LogInit()
	inits.DBInit()
	sqlDB, err := global.DB.DB()
	if err != nil {
		return fmt.Errorf("access database connection: %w", err)
	}
	defer func() {
		if closeErr := sqlDB.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close database connection: %w", closeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.CheckAgentWorkflowDatabase(ctx, global.DB); err != nil {
		return fmt.Errorf("check agent workflow database: %w", err)
	}

	return json.NewEncoder(os.Stdout).Encode(struct {
		WorkflowStore     string `json:"workflow_store"`
		DatabaseRequired  bool   `json:"database_required"`
		DatabaseReady     bool   `json:"database_ready"`
		ConfigFingerprint string `json:"config_fingerprint"`
	}{cfg.WorkflowStore, true, true, cfg.Fingerprint()})
}

func runAgentConfigCheck() (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("load config: %v", recovered)
		}
	}()
	inits.ConfigInit()
	cfg, err := app.ParseAgentRuntimeConfig(global.AppConfig.LLM, global.AppConfig.Env)
	if err != nil {
		return err
	}
	migrationDeadline := ""
	if !cfg.WorkflowMigrationDeadline.IsZero() {
		migrationDeadline = cfg.WorkflowMigrationDeadline.UTC().Format(time.RFC3339)
	}
	report := struct {
		Environment               string `json:"environment"`
		ProtocolMode              string `json:"protocol_mode"`
		CompilerTimeout           string `json:"compiler_timeout"`
		Model                     string `json:"model"`
		IntentResponseFormat      string `json:"intent_response_format"`
		DeterministicCompilerMode string `json:"deterministic_compiler_mode"`
		IntentContextEnabled      bool   `json:"intent_context_enabled"`
		WorkflowStore             string `json:"workflow_store"`
		WorkflowMigration         bool   `json:"workflow_migration"`
		WorkflowMigrationDeadline string `json:"workflow_migration_deadline,omitempty"`
		LogPayloads               bool   `json:"log_payloads"`
		ConfigFingerprint         string `json:"config_fingerprint"`
	}{
		Environment:               global.AppConfig.Env,
		ProtocolMode:              cfg.ProtocolMode,
		CompilerTimeout:           cfg.IntentCompilerTimeout.String(),
		Model:                     cfg.Model,
		IntentResponseFormat:      cfg.IntentResponseFormat,
		DeterministicCompilerMode: cfg.DeterministicCompilerMode,
		IntentContextEnabled:      cfg.IntentContextEnabled,
		WorkflowStore:             cfg.WorkflowStore,
		WorkflowMigration:         cfg.WorkflowMigration,
		WorkflowMigrationDeadline: migrationDeadline,
		LogPayloads:               cfg.LogPayloads,
		ConfigFingerprint:         cfg.Fingerprint(),
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return errors.New("encode validation report")
	}
	return nil
}
