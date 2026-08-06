package agent

import "testing"

func mustNewTestAgent(deps Deps) *Agent {
	if deps.WorkflowStore == nil {
		deps.WorkflowStore = newMemoryWorkflowStore(nil)
	}
	if deps.ProtocolMode == "" {
		deps.ProtocolMode = string(ProtocolModeLegacy)
	}
	constructed, err := NewAgent(deps)
	if err != nil {
		panic(err)
	}
	return constructed
}

func TestNewAgentRejectsMissingWorkflowStore(t *testing.T) {
	t.Parallel()

	constructed, err := NewAgent(Deps{ProtocolMode: string(ProtocolModeLegacy)})
	if err == nil || constructed != nil {
		t.Fatalf("NewAgent() = %v, %v; want nil, error", constructed, err)
	}
}
