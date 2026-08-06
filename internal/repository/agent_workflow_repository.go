package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"schedule_server/internal/model"
	"schedule_server/internal/tenantctx"

	"gorm.io/gorm"
)

const (
	AgentWorkflowExecutionIdle             = "idle"
	AgentWorkflowExecutionExecuting        = "executing"
	AgentWorkflowExecutionResultRecorded   = "result_recorded"
	AgentWorkflowExecutionRecoveryRequired = "recovery_required"

	idleExecutionPredicate = `execution_status = ?
		AND execution_token IS NULL
		AND execution_operation IS NULL
		AND business_key IS NULL
		AND request_id IS NULL
		AND execution_request_schema_version IS NULL
		AND execution_request_json IS NULL
		AND execution_result_schema_version IS NULL
		AND execution_result_json IS NULL
		AND write_effect IS NULL
		AND lease_expires_at IS NULL`
)

var (
	ErrAgentWorkflowConflict       = errors.New("agent workflow version conflict")
	ErrAgentWorkflowTenantMismatch = errors.New("agent workflow tenant mismatch")
)

// AgentWorkflowKey is the unique, tenant-scoped workflow identity.
type AgentWorkflowKey struct {
	TenantID       uint
	ConversationID string
	ActorUserID    uint
}

// AgentWorkflowSnapshotUpdate contains only the business snapshot projection.
// Execution authority is deliberately updated through token-aware methods.
type AgentWorkflowSnapshotUpdate struct {
	WorkflowID            string
	WorkflowType          string
	WorkflowState         string
	SnapshotSchemaVersion uint16
	SnapshotJSON          string
	ExpiresAt             time.Time
}

// AgentWorkflowReservation contains the persisted execution request and lease.
type AgentWorkflowReservation struct {
	ExecutionToken       string
	ExecutionOperation   string
	BusinessKey          string
	RequestID            string
	RequestSchemaVersion uint16
	RequestJSON          string
	LeaseExpiresAt       time.Time
}

// AgentWorkflowExecutionResult contains the stable result written before
// finalization.
type AgentWorkflowExecutionResult struct {
	ResultSchemaVersion uint16
	ResultJSON          string
	BusinessKey         string
	WriteEffect         string
	CompletedAt         time.Time
}

type AgentWorkflowRepository interface {
	Load(ctx context.Context, key AgentWorkflowKey) (*model.AgentWorkflow, error)
	Create(ctx context.Context, workflow *model.AgentWorkflow) error
	CompareAndSwap(
		ctx context.Context,
		key AgentWorkflowKey,
		expectedVersion uint64,
		next AgentWorkflowSnapshotUpdate,
	) error
	DeleteIfVersion(ctx context.Context, key AgentWorkflowKey, expectedVersion uint64) error
	CreateReservedExecution(
		ctx context.Context,
		workflow *model.AgentWorkflow,
		reservation AgentWorkflowReservation,
	) error
	ReserveExecution(
		ctx context.Context,
		key AgentWorkflowKey,
		expectedVersion uint64,
		next AgentWorkflowSnapshotUpdate,
		reservation AgentWorkflowReservation,
	) error
	RecordExecutionResult(
		ctx context.Context,
		key AgentWorkflowKey,
		expectedVersion uint64,
		executionToken string,
		result AgentWorkflowExecutionResult,
	) error
	FinalizeExecution(
		ctx context.Context,
		key AgentWorkflowKey,
		expectedVersion uint64,
		executionToken string,
		next AgentWorkflowSnapshotUpdate,
	) error
	DeleteReservedExecution(
		ctx context.Context,
		key AgentWorkflowKey,
		expectedVersion uint64,
		executionToken string,
	) error
	TakeoverExpiredExecution(
		ctx context.Context,
		key AgentWorkflowKey,
		expectedVersion uint64,
		previousToken string,
		expiredBefore time.Time,
		next AgentWorkflowReservation,
	) error
	ListRecoverableExecutions(ctx context.Context, now time.Time, limit int) ([]model.AgentWorkflow, error)
	MarkExecutionRecoveryRequired(
		ctx context.Context,
		key AgentWorkflowKey,
		expectedVersion uint64,
		executionToken string,
		retryAt time.Time,
	) error
}

type agentWorkflowRepository struct {
	db *gorm.DB
}

func NewAgentWorkflowRepository(db *gorm.DB) AgentWorkflowRepository {
	return &agentWorkflowRepository{db: db}
}

func (r *agentWorkflowRepository) Load(
	ctx context.Context,
	key AgentWorkflowKey,
) (*model.AgentWorkflow, error) {
	if err := r.validate(ctx, key); err != nil {
		return nil, err
	}
	var workflow model.AgentWorkflow
	err := r.db.WithContext(ctx).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		First(&workflow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &workflow, nil
}

func (r *agentWorkflowRepository) Create(
	ctx context.Context,
	workflow *model.AgentWorkflow,
) error {
	if workflow == nil {
		return errors.New("repository: agent workflow is nil")
	}
	if err := r.validate(ctx, keyFromWorkflow(workflow)); err != nil {
		return err
	}
	if err := validateWorkflowForCreate(workflow); err != nil {
		return err
	}
	prepareNewWorkflow(workflow, AgentWorkflowExecutionIdle)
	return requireOneCreatedRow(r.db.WithContext(ctx).Create(workflow))
}

func (r *agentWorkflowRepository) CompareAndSwap(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
	next AgentWorkflowSnapshotUpdate,
) error {
	if err := r.validateMutation(ctx, key, expectedVersion); err != nil {
		return err
	}
	if err := validateSnapshotUpdate(next); err != nil {
		return err
	}
	updates := snapshotUpdates(next)
	updates["version"] = gorm.Expr("version + ?", 1)
	return requireOneRow(r.db.WithContext(ctx).
		Model(&model.AgentWorkflow{}).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		Where("version = ? AND "+idleExecutionPredicate, expectedVersion, AgentWorkflowExecutionIdle).
		Updates(updates))
}

func (r *agentWorkflowRepository) DeleteIfVersion(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
) error {
	if err := r.validateMutation(ctx, key, expectedVersion); err != nil {
		return err
	}
	return requireOneRow(r.db.WithContext(ctx).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		Where("version = ? AND "+idleExecutionPredicate, expectedVersion, AgentWorkflowExecutionIdle).
		Delete(&model.AgentWorkflow{}))
}

func (r *agentWorkflowRepository) CreateReservedExecution(
	ctx context.Context,
	workflow *model.AgentWorkflow,
	reservation AgentWorkflowReservation,
) error {
	if workflow == nil {
		return errors.New("repository: agent workflow is nil")
	}
	if err := r.validate(ctx, keyFromWorkflow(workflow)); err != nil {
		return err
	}
	if err := validateWorkflowForCreate(workflow); err != nil {
		return err
	}
	if err := validateReservation(reservation); err != nil {
		return err
	}
	prepareNewWorkflow(workflow, AgentWorkflowExecutionExecuting)
	applyReservationToWorkflow(workflow, reservation)
	return requireOneCreatedRow(r.db.WithContext(ctx).Create(workflow))
}

func (r *agentWorkflowRepository) ReserveExecution(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
	next AgentWorkflowSnapshotUpdate,
	reservation AgentWorkflowReservation,
) error {
	if err := r.validateMutation(ctx, key, expectedVersion); err != nil {
		return err
	}
	if err := validateSnapshotUpdate(next); err != nil {
		return err
	}
	if err := validateReservation(reservation); err != nil {
		return err
	}
	updates := snapshotUpdates(next)
	addReservationUpdates(updates, reservation)
	updates["execution_status"] = AgentWorkflowExecutionExecuting
	updates["execution_result_schema_version"] = nil
	updates["execution_result_json"] = nil
	updates["write_effect"] = nil
	updates["version"] = gorm.Expr("version + ?", 1)
	return requireOneRow(r.db.WithContext(ctx).
		Model(&model.AgentWorkflow{}).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		Where("version = ? AND "+idleExecutionPredicate, expectedVersion, AgentWorkflowExecutionIdle).
		Updates(updates))
}

func (r *agentWorkflowRepository) RecordExecutionResult(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
	executionToken string,
	result AgentWorkflowExecutionResult,
) error {
	if err := r.validateMutation(ctx, key, expectedVersion); err != nil {
		return err
	}
	if strings.TrimSpace(executionToken) == "" {
		return errors.New("repository: execution token is empty")
	}
	if result.ResultSchemaVersion == 0 ||
		strings.TrimSpace(result.ResultJSON) == "" ||
		strings.TrimSpace(result.BusinessKey) == "" ||
		strings.TrimSpace(result.WriteEffect) == "" ||
		result.CompletedAt.IsZero() {
		return errors.New("repository: execution result is incomplete")
	}
	return requireOneRow(r.db.WithContext(ctx).
		Model(&model.AgentWorkflow{}).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		Where(
			`version = ? AND execution_status = ? AND execution_token = ? AND business_key = ?
				AND lease_expires_at IS NOT NULL
				AND execution_request_schema_version IS NOT NULL
				AND execution_request_json IS NOT NULL`,
			expectedVersion,
			AgentWorkflowExecutionExecuting,
			executionToken,
			result.BusinessKey,
		).
		Updates(map[string]any{
			"execution_status":                AgentWorkflowExecutionResultRecorded,
			"execution_result_schema_version": result.ResultSchemaVersion,
			"execution_result_json":           result.ResultJSON,
			"write_effect":                    nullableString(result.WriteEffect),
			"version":                         gorm.Expr("version + ?", 1),
		}))
}

func (r *agentWorkflowRepository) FinalizeExecution(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
	executionToken string,
	next AgentWorkflowSnapshotUpdate,
) error {
	if err := r.validateMutation(ctx, key, expectedVersion); err != nil {
		return err
	}
	if err := validateSnapshotUpdate(next); err != nil {
		return err
	}
	if strings.TrimSpace(executionToken) == "" {
		return errors.New("repository: execution token is empty")
	}
	updates := snapshotUpdates(next)
	clearExecutionUpdates(updates)
	updates["execution_status"] = AgentWorkflowExecutionIdle
	updates["version"] = gorm.Expr("version + ?", 1)
	return requireOneRow(r.db.WithContext(ctx).
		Model(&model.AgentWorkflow{}).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		Where(
			`version = ? AND execution_status = ? AND execution_token = ?
				AND execution_result_schema_version IS NOT NULL
				AND execution_result_json IS NOT NULL`,
			expectedVersion,
			AgentWorkflowExecutionResultRecorded,
			executionToken,
		).
		Updates(updates))
}

func (r *agentWorkflowRepository) DeleteReservedExecution(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
	executionToken string,
) error {
	if err := r.validateMutation(ctx, key, expectedVersion); err != nil {
		return err
	}
	if strings.TrimSpace(executionToken) == "" {
		return errors.New("repository: execution token is empty")
	}
	return requireOneRow(r.db.WithContext(ctx).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		Where(
			`version = ? AND execution_token = ? AND execution_status = ?
				AND execution_result_schema_version IS NOT NULL
				AND execution_result_json IS NOT NULL`,
			expectedVersion,
			executionToken,
			AgentWorkflowExecutionResultRecorded,
		).
		Delete(&model.AgentWorkflow{}))
}

func (r *agentWorkflowRepository) TakeoverExpiredExecution(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
	previousToken string,
	expiredBefore time.Time,
	next AgentWorkflowReservation,
) error {
	if err := r.validateMutation(ctx, key, expectedVersion); err != nil {
		return err
	}
	if strings.TrimSpace(previousToken) == "" || expiredBefore.IsZero() {
		return errors.New("repository: takeover predicate is incomplete")
	}
	if err := validateReservation(next); err != nil {
		return err
	}
	updates := make(map[string]any)
	addReservationUpdates(updates, next)
	updates["execution_status"] = AgentWorkflowExecutionExecuting
	updates["execution_result_schema_version"] = nil
	updates["execution_result_json"] = nil
	updates["write_effect"] = nil
	updates["version"] = gorm.Expr("version + ?", 1)
	return requireOneRow(r.db.WithContext(ctx).
		Model(&model.AgentWorkflow{}).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		Where("version = ? AND execution_token = ?", expectedVersion, previousToken).
		Where(
			"execution_status IN ?",
			[]string{
				AgentWorkflowExecutionExecuting,
				AgentWorkflowExecutionRecoveryRequired,
			},
		).
		Where(
			`execution_operation = ? AND business_key = ?
				AND execution_request_schema_version IS NOT NULL
				AND execution_request_json IS NOT NULL
				AND lease_expires_at IS NOT NULL
				AND lease_expires_at <= ?`,
			next.ExecutionOperation,
			next.BusinessKey,
			expiredBefore,
		).
		Updates(updates))
}

func (r *agentWorkflowRepository) ListRecoverableExecutions(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]model.AgentWorkflow, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository: agent workflow database is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() || limit <= 0 || limit > 100 {
		return nil, errors.New("repository: invalid workflow recovery scan")
	}
	var workflows []model.AgentWorkflow
	err := r.db.WithContext(tenantctx.WithSkipTenantScope(ctx)).
		Where(
			`execution_status = ? OR (
				execution_status IN ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?
			)`,
			AgentWorkflowExecutionResultRecorded,
			[]string{AgentWorkflowExecutionExecuting, AgentWorkflowExecutionRecoveryRequired},
			now.UTC(),
		).
		Order("updated_at ASC").
		Limit(limit).
		Find(&workflows).Error
	return workflows, err
}

func (r *agentWorkflowRepository) MarkExecutionRecoveryRequired(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
	executionToken string,
	retryAt time.Time,
) error {
	if err := r.validateMutation(ctx, key, expectedVersion); err != nil {
		return err
	}
	if strings.TrimSpace(executionToken) == "" || retryAt.IsZero() {
		return errors.New("repository: recovery deferral predicate is incomplete")
	}
	return requireOneRow(r.db.WithContext(ctx).
		Model(&model.AgentWorkflow{}).
		Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
		Where(
			"version = ? AND execution_token = ? AND execution_status = ?",
			expectedVersion,
			executionToken,
			AgentWorkflowExecutionExecuting,
		).
		Updates(map[string]any{
			"execution_status": AgentWorkflowExecutionRecoveryRequired,
			"lease_expires_at": retryAt.UTC(),
			"version":          gorm.Expr("version + ?", 1),
		}))
}

func (r *agentWorkflowRepository) validate(ctx context.Context, key AgentWorkflowKey) error {
	if r == nil || r.db == nil {
		return errors.New("repository: agent workflow database is nil")
	}
	if ctx == nil {
		return ErrTenantMissing
	}
	tenantID, err := tenantIDFromCtx(ctx)
	if err != nil {
		return err
	}
	if tenantID != key.TenantID {
		return ErrAgentWorkflowTenantMismatch
	}
	if key.TenantID == 0 || strings.TrimSpace(key.ConversationID) == "" {
		return errors.New("repository: agent workflow key is incomplete")
	}
	return nil
}

func (r *agentWorkflowRepository) validateMutation(
	ctx context.Context,
	key AgentWorkflowKey,
	expectedVersion uint64,
) error {
	if err := r.validate(ctx, key); err != nil {
		return err
	}
	if expectedVersion == 0 {
		return errors.New("repository: expected workflow version is zero")
	}
	return nil
}

func validateReservation(reservation AgentWorkflowReservation) error {
	if strings.TrimSpace(reservation.ExecutionToken) == "" ||
		strings.TrimSpace(reservation.ExecutionOperation) == "" ||
		strings.TrimSpace(reservation.BusinessKey) == "" ||
		strings.TrimSpace(reservation.RequestID) == "" ||
		reservation.RequestSchemaVersion == 0 ||
		strings.TrimSpace(reservation.RequestJSON) == "" ||
		reservation.LeaseExpiresAt.IsZero() {
		return errors.New("repository: execution reservation is incomplete")
	}
	return nil
}

func validateWorkflowForCreate(workflow *model.AgentWorkflow) error {
	return validateSnapshotUpdate(AgentWorkflowSnapshotUpdate{
		WorkflowID:            workflow.WorkflowID,
		WorkflowType:          workflow.WorkflowType,
		WorkflowState:         workflow.WorkflowState,
		SnapshotSchemaVersion: workflow.SnapshotSchemaVersion,
		SnapshotJSON:          workflow.SnapshotJSON,
		ExpiresAt:             workflow.ExpiresAt,
	})
}

func validateSnapshotUpdate(next AgentWorkflowSnapshotUpdate) error {
	if strings.TrimSpace(next.WorkflowID) == "" ||
		strings.TrimSpace(next.WorkflowType) == "" ||
		strings.TrimSpace(next.WorkflowState) == "" ||
		next.SnapshotSchemaVersion == 0 ||
		strings.TrimSpace(next.SnapshotJSON) == "" ||
		next.ExpiresAt.IsZero() {
		return errors.New("repository: workflow snapshot update is incomplete")
	}
	return nil
}

func prepareNewWorkflow(workflow *model.AgentWorkflow, status string) {
	workflow.Version = 1
	if workflow.SnapshotSchemaVersion == 0 {
		workflow.SnapshotSchemaVersion = 1
	}
	workflow.ExecutionStatus = status
	if status == AgentWorkflowExecutionIdle {
		workflow.ExecutionToken = nil
		workflow.ExecutionOperation = nil
		workflow.BusinessKey = nil
		workflow.RequestID = nil
		workflow.ExecutionRequestSchemaVersion = nil
		workflow.ExecutionRequestJSON = nil
		workflow.ExecutionResultSchemaVersion = nil
		workflow.ExecutionResultJSON = nil
		workflow.WriteEffect = nil
		workflow.LeaseExpiresAt = nil
	}
}

func applyReservationToWorkflow(
	workflow *model.AgentWorkflow,
	reservation AgentWorkflowReservation,
) {
	workflow.ExecutionToken = stringPointer(reservation.ExecutionToken)
	workflow.ExecutionOperation = stringPointer(reservation.ExecutionOperation)
	workflow.BusinessKey = stringPointer(reservation.BusinessKey)
	workflow.RequestID = stringPointer(reservation.RequestID)
	workflow.ExecutionRequestSchemaVersion = uint16Pointer(reservation.RequestSchemaVersion)
	workflow.ExecutionRequestJSON = stringPointer(reservation.RequestJSON)
	workflow.ExecutionResultSchemaVersion = nil
	workflow.ExecutionResultJSON = nil
	workflow.WriteEffect = nil
	workflow.LeaseExpiresAt = timePointer(reservation.LeaseExpiresAt)
}

func snapshotUpdates(next AgentWorkflowSnapshotUpdate) map[string]any {
	return map[string]any{
		"workflow_id":             next.WorkflowID,
		"workflow_type":           next.WorkflowType,
		"workflow_state":          next.WorkflowState,
		"snapshot_schema_version": next.SnapshotSchemaVersion,
		"snapshot_json":           next.SnapshotJSON,
		"expires_at":              next.ExpiresAt,
	}
}

func addReservationUpdates(updates map[string]any, reservation AgentWorkflowReservation) {
	updates["execution_token"] = reservation.ExecutionToken
	updates["execution_operation"] = reservation.ExecutionOperation
	updates["business_key"] = reservation.BusinessKey
	updates["request_id"] = reservation.RequestID
	updates["execution_request_schema_version"] = reservation.RequestSchemaVersion
	updates["execution_request_json"] = reservation.RequestJSON
	updates["lease_expires_at"] = reservation.LeaseExpiresAt
}

func clearExecutionUpdates(updates map[string]any) {
	updates["execution_token"] = nil
	updates["execution_operation"] = nil
	updates["business_key"] = nil
	updates["request_id"] = nil
	updates["execution_request_schema_version"] = nil
	updates["execution_request_json"] = nil
	updates["execution_result_schema_version"] = nil
	updates["execution_result_json"] = nil
	updates["write_effect"] = nil
	updates["lease_expires_at"] = nil
}

func workflowKeyWhere() string {
	return "tenant_id = ? AND conversation_id = ? AND actor_user_id = ?"
}

func keyFromWorkflow(workflow *model.AgentWorkflow) AgentWorkflowKey {
	return AgentWorkflowKey{
		TenantID:       workflow.TenantID,
		ConversationID: workflow.ConversationID,
		ActorUserID:    workflow.ActorUserID,
	}
}

func requireOneRow(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAgentWorkflowConflict
	}
	return nil
}

func requireOneCreatedRow(result *gorm.DB) error {
	if err := mapAgentWorkflowCreateError(result); err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrAgentWorkflowConflict
	}
	return nil
}

func mapAgentWorkflowCreateError(result *gorm.DB) error {
	err := result.Error
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrAgentWorkflowConflict
	}
	if translator, ok := result.Dialector.(gorm.ErrorTranslator); ok {
		if errors.Is(translator.Translate(err), gorm.ErrDuplicatedKey) {
			return ErrAgentWorkflowConflict
		}
	}
	// SQLite is used only by repository tests and does not translate errors
	// unless TranslateError is enabled on that test connection.
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrAgentWorkflowConflict
	}
	return err
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func stringPointer(value string) *string     { return &value }
func uint16Pointer(value uint16) *uint16     { return &value }
func timePointer(value time.Time) *time.Time { return &value }

var _ AgentWorkflowRepository = (*agentWorkflowRepository)(nil)
