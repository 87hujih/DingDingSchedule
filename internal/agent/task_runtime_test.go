package agent

import "testing"

type testRuntimeHandler struct {
	taskType string
}

func (h testRuntimeHandler) Type() string {
	return h.taskType
}

func TestRuntimeDispatchFallsBackForUnmigratedTask(t *testing.T) {
	t.Parallel()

	runtime := newTaskRuntime(nil)
	result := runtime.Dispatch(TaskInstance{Type: "weekly_absence_ranking"})

	if result.FallbackReason == "" {
		t.Fatalf("FallbackReason = empty, want fallback for unmigrated task")
	}
	if result.Handler != nil {
		t.Fatalf("Handler = %#v, want nil", result.Handler)
	}
}

func TestRuntimeDispatchResolvesRegisteredHandler(t *testing.T) {
	t.Parallel()

	runtime := newTaskRuntime([]TaskHandler{
		testRuntimeHandler{taskType: "subscribe_attendance_push"},
	})
	result := runtime.Dispatch(TaskInstance{Type: "subscribe_attendance_push"})

	if result.FallbackReason != "" {
		t.Fatalf("FallbackReason = %q, want empty", result.FallbackReason)
	}
	if result.Handler == nil {
		t.Fatalf("Handler = nil, want registered handler")
	}
	if result.Handler.Type() != "subscribe_attendance_push" {
		t.Fatalf("Handler.Type() = %q, want subscribe_attendance_push", result.Handler.Type())
	}
}
