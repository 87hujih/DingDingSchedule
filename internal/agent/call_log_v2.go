package agent

// FailureLayer is the stable protocol-live failure classification written to CallLog V2.
type FailureLayer string

const (
	FailureIngress              FailureLayer = "ingress_failed"
	FailureIntent               FailureLayer = "intent_failed"
	FailureCatalog              FailureLayer = "catalog_failed"
	FailureWorkflow             FailureLayer = "workflow_failed"
	FailureEntityAmbiguous      FailureLayer = "entity_ambiguous"
	FailureEntityNotFound       FailureLayer = "entity_not_found"
	FailurePrePolicyDenied      FailureLayer = "pre_policy_denied"
	FailureResourcePolicyDenied FailureLayer = "resource_policy_denied"
	FailureWriteGuardBlocked    FailureLayer = "write_guard_blocked"
	FailureExecutor             FailureLayer = "executor_failed"
	FailureRenderer             FailureLayer = "renderer_failed"
	FailurePersistence          FailureLayer = "persistence_failed"
)

func failureLayers() []FailureLayer {
	return []FailureLayer{
		FailureIngress,
		FailureIntent,
		FailureCatalog,
		FailureWorkflow,
		FailureEntityAmbiguous,
		FailureEntityNotFound,
		FailurePrePolicyDenied,
		FailureResourcePolicyDenied,
		FailureWriteGuardBlocked,
		FailureExecutor,
		FailureRenderer,
		FailurePersistence,
	}
}
