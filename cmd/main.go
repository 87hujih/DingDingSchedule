package main

import (
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
	if len(os.Args) == 2 && os.Args[1] == "agent-config-check" {
		if err := runAgentConfigCheck(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "agent config invalid: %v\n", err)
			os.Exit(1)
		}
		return
	}
	inits.Init()
	if err := app.RunServer(); err != nil {
		global.Log.Fatalw("服务启动失败", "error", err)
	}
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
