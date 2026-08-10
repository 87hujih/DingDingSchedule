package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolLiveBoundaryHasNoOldProtocolPrimaryPath(t *testing.T) {
	t.Parallel()

	source := readAgentSourceForBoundaryTest(t, "agent.go")
	forbidden := []string{
		"tryHandleProtocolPrimary",
		"handleProtocolManualSignPrimary",
		"handleProtocolSubscriptionPrimary",
		"handleProtocolFallback",
		"protocol_live_write",
		"SignForUsersBySlot",
	}
	for _, symbol := range forbidden {
		if strings.Contains(source, symbol) {
			t.Fatalf("agent.go still contains old protocol primary symbol %q; protocol_live must use protocolLivePipeline only", symbol)
		}
	}
}

func TestProtocolLiveImplementationDoesNotReferenceLegacyDispatchers(t *testing.T) {
	t.Parallel()

	liveFiles := []string{
		"protocol_live_pipeline.go",
		"protocol_live_dispatch.go",
		"operation_executor.go",
		"response_renderer.go",
		"policy_gate.go",
		"workflow_engine.go",
		"intent_compiler.go",
	}
	forbidden := []string{
		"chatWithSemanticRouter",
		"chatLegacy",
		"tryHandleRoutePrimary",
		"runReactLoop",
		"newTaskCatalog",
		"taskCatalog",
		"taskRouter",
		"selectToolPool",
		"toolPool",
		"tools.Registry",
		"fallbackQueryKind",
		"RouteSourceFallback",
		"RouteSourceSemanticRouter",
		"planConversation",
		"sign_for_user",
		"SignForUsersBySlot",
		"handleProtocolManualSignPrimary",
	}

	for _, file := range liveFiles {
		source := readAgentSourceForBoundaryTest(t, file)
		for _, symbol := range forbidden {
			if strings.Contains(source, symbol) {
				t.Fatalf("%s references legacy symbol %q; protocol_live must stay on catalog/pipeline boundaries", file, symbol)
			}
		}
	}
}

func TestProtocolLiveBusinessIntentCompilerDoesNotUseKeywordRouters(t *testing.T) {
	t.Parallel()

	source := readAgentSourceForBoundaryTest(t, "operation_compiler.go")
	for _, symbol := range []string{
		"catalogAliasCandidates",
		"catalogDetectorCandidates",
		"compileScheduleReadQuery",
		"looksLikeAttendanceReadQuery",
		"looksLikeSubscriptionStatusQuery",
		"looksLikeSubscriptionWriteRequest",
	} {
		if strings.Contains(source, symbol) {
			t.Fatalf("operation_compiler.go references %q; protocol_live business intent must come from the semantic compiler", symbol)
		}
	}
}

func TestRetainedBoundaryFilesDeclareTheirScope(t *testing.T) {
	t.Parallel()

	requiredMarkers := map[string]string{
		"query_router.go":       "legacy-only",
		"planner.go":            "legacy-only",
		"planner_contract.go":   "legacy-only",
		"planner_service.go":    "legacy-only",
		"planner_types.go":      "legacy-only",
		"react_loop.go":         "legacy-only",
		"task_catalog.go":       "legacy-only",
		"task_router.go":        "legacy-only",
		"tool_pool.go":          "legacy-only",
		"capability_catalog.go": "OperationCatalog-derived capability view",
	}
	for file, marker := range requiredMarkers {
		if source := readAgentSourceForBoundaryTest(t, file); !strings.Contains(source, marker) {
			t.Fatalf("%s must declare %q to keep protocol_live and legacy boundaries explicit", file, marker)
		}
	}
}

func readAgentSourceForBoundaryTest(t *testing.T, name string) string {
	t.Helper()

	source, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", name, err)
	}
	return string(source)
}
