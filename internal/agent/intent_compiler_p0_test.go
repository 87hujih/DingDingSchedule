package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"schedule_server/internal/agent/tools"
)

func TestIntentCompilerRequestsJSONObjectResponse(t *testing.T) {
	t.Parallel()

	client := &capturingStructuredChatClient{
		content: `{"act":"help","domain":"system","operation":"system.describe_capability","confidence":1,"slots":[],"reason":"help"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{
		Timeout: 5 * time.Second,
		StructuredOutput: StructuredOutputSpec{
			Mode:                 "json_object",
			Temperature:          0,
			MaxTokens:            512,
			TransportMaxAttempts: 2,
			ParseRepairAttempts:  0,
		},
	})

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "你有什么功能"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Status != IntentCompileOK {
		t.Fatalf("Status = %q, want %q", result.Status, IntentCompileOK)
	}
	if client.spec.Mode != "json_object" {
		t.Fatalf("Mode = %q, want json_object", client.spec.Mode)
	}
	if client.spec.Temperature != 0 || client.spec.MaxTokens != 512 {
		t.Fatalf("spec = %+v, want temperature=0 max_tokens=512", client.spec)
	}
	if client.spec.TransportMaxAttempts != 2 || client.spec.ParseRepairAttempts != 0 {
		t.Fatalf("spec = %+v, want bounded transport attempts and no repair", client.spec)
	}
}

func TestIntentCompilerDistinguishesUnknownFromInvalidOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		status  IntentCompileStatus
	}{
		{
			name:    "model unknown",
			content: `{"act":"unknown","domain":"unknown","operation":"","confidence":0.1,"slots":[],"reason":"unknown_intent"}`,
			status:  IntentCompileUnknown,
		},
		{
			name:    "invalid output",
			content: `not-json`,
			status:  IntentCompileInvalidOutput,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			compiler := newLLMIntentCompiler(&capturingStructuredChatClient{content: tt.content}, intentCompilerOptions{})
			result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "随便问问"})
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if result.Status != tt.status {
				t.Fatalf("Status = %q, want %q", result.Status, tt.status)
			}
		})
	}
}

func TestIntentCompilerReturnsTypedTransportStatus(t *testing.T) {
	t.Parallel()

	compiler := newLLMIntentCompiler(&capturingStructuredChatClient{
		err:      errors.New("upstream unavailable"),
		attempts: 2,
	}, intentCompilerOptions{})

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查考勤"})
	if err != nil {
		t.Fatalf("Compile() error = %v, want typed transport result", err)
	}
	if result.Status != IntentCompileTransportError || result.Attempts != 2 {
		t.Fatalf("result = %+v, want transport_error attempts=2", result)
	}
}

func TestIntentCompilerChildTimeoutReturnsTypedStatus(t *testing.T) {
	t.Parallel()

	compiler := newLLMIntentCompiler(blockingStructuredChatClient{}, intentCompilerOptions{Timeout: 10 * time.Millisecond})
	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查考勤"})
	if err != nil {
		t.Fatalf("Compile() error = %v, want typed child timeout", err)
	}
	if result.Status != IntentCompileTimeout {
		t.Fatalf("Status = %q, want %q", result.Status, IntentCompileTimeout)
	}
}

func TestIntentCompilerParentCancellationIsLifecycleError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	compiler := newLLMIntentCompiler(blockingStructuredChatClient{}, intentCompilerOptions{Timeout: time.Second})

	_, err := compiler.Compile(ctx, IntentCompileRequest{Message: "查考勤"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Compile() error = %v, want context.Canceled", err)
	}
}

func TestIntentCompilerIncludesOnlyBoundedRecentConversation(t *testing.T) {
	t.Parallel()

	client := &capturingStructuredChatClient{
		content: `{"act":"read_query","domain":"schedule","operation":"schedule.query_my_schedule","confidence":0.9,"slots":[],"reason":"follow-up"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})
	history := []tools.Message{
		{Role: "tool", Content: "internal tool output must not leak"},
		{Role: "user", Content: "帮我查明天的课表"},
		{Role: "assistant", Content: "你是想查询自己的课表吗？"},
	}

	_, err := compiler.Compile(context.Background(), IntentCompileRequest{
		Message:        "对，我的",
		RecentMessages: history,
		ActiveWorkflow: &IntentCompileWorkflowContext{
			Type:          "subscription.start",
			State:         "collect_scope",
			MissingFields: []string{"scope"},
			Candidates:    map[string][]string{"dept_ids": {"第一部门", "第二部门"}},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(client.messages) != 4 {
		t.Fatalf("messages = %#v, want system + 2 history + current envelope", client.messages)
	}
	if client.messages[1].Role != "user" || client.messages[2].Role != "assistant" {
		t.Fatalf("history roles = %q/%q, want user/assistant", client.messages[1].Role, client.messages[2].Role)
	}
	for _, message := range client.messages {
		if strings.Contains(message.Content, "internal tool output") {
			t.Fatalf("tool output leaked into compiler context: %#v", client.messages)
		}
	}
	current := client.messages[3]
	if current.Role != "user" || !strings.Contains(current.Content, `"current_message":"对，我的"`) {
		t.Fatalf("current envelope = %+v", current)
	}
	if !strings.Contains(current.Content, `"workflow_context"`) ||
		!strings.Contains(current.Content, `"第一部门"`) {
		t.Fatalf("workflow envelope = %s", current.Content)
	}
	if !strings.Contains(client.messages[0].Content, "current_message 永远是本次唯一分类目标") {
		t.Fatalf("system prompt lacks untrusted-history boundary: %s", client.messages[0].Content)
	}
}

type capturingStructuredChatClient struct {
	content  string
	err      error
	attempts int
	spec     StructuredOutputSpec
	messages []tools.Message
}

func (c *capturingStructuredChatClient) ChatStructured(
	_ context.Context,
	messages []tools.Message,
	spec StructuredOutputSpec,
) (StructuredChatResponse, error) {
	c.spec = spec
	c.messages = append([]tools.Message(nil), messages...)
	attempts := c.attempts
	if attempts == 0 {
		attempts = 1
	}
	return StructuredChatResponse{
		Message:  tools.Message{Role: "assistant", Content: c.content},
		Attempts: attempts,
	}, c.err
}

type blockingStructuredChatClient struct{}

func (blockingStructuredChatClient) ChatStructured(
	ctx context.Context,
	_ []tools.Message,
	_ StructuredOutputSpec,
) (StructuredChatResponse, error) {
	<-ctx.Done()
	return StructuredChatResponse{}, ctx.Err()
}
