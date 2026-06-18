package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"schedule_server/internal/agent/tools"
)

func TestIntentCompilerParsesStrictJSONDraft(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"read_query","domain":"attendance","operation":"attendance.query_status","confidence":0.91,"slots":[{"field":"date","raw":"今天"},{"field":"section","raw":"第二节"}],"reason":"用户查询考勤状态"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActReadQuery || draft.Operation != "attendance.query_status" {
		t.Fatalf("draft = %+v", draft)
	}
	if draft.Slots["date"].Raw != "今天" || draft.Slots["section"].Raw != "第二节" {
		t.Fatalf("slots = %+v", draft.Slots)
	}
	if draft.Params != nil || len(draft.MissingFields) != 0 {
		t.Fatalf("compiler must not trust raw slots as resolved params: %+v", draft)
	}
}

func TestIntentCompilerReturnsUnknownForInvalidJSON(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{content: `不是 JSON`}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActUnknown || draft.Reason != "intent_parse_failed" {
		t.Fatalf("draft = %+v, want ActUnknown with intent_parse_failed", draft)
	}
}

func TestIntentCompilerPreservesOperationOutsideCatalogForValidator(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"write_request","domain":"manual_sign","operation":"manual_sign.create","confidence":0.94,"slots":[],"reason":"用户要求补签"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "帮张三补签"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActWriteRequest || draft.Domain != DomainManualSign || draft.Operation != "manual_sign.create" {
		t.Fatalf("draft = %+v, want untrusted catalog-missing draft for validator", draft)
	}
}

func TestIntentCompilerPreservesSchemaUnknownDraft(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"unknown","domain":"unknown","operation":"","confidence":0.23,"slots":[],"reason":"unknown_intent"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "最近怎么样"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActUnknown || draft.Domain != DomainUnknown || draft.Reason != "unknown_intent" {
		t.Fatalf("draft = %+v, want schema-valid unknown draft", draft)
	}
}

func TestIntentCompilerStripsOperationFromUnknownDraft(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"unknown","domain":"unknown","operation":"subscription.cancel","confidence":0.23,"slots":[],"reason":"unknown_intent"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "随便"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActUnknown || draft.Operation != "" || draft.Reason != "unknown_intent" {
		t.Fatalf("draft = %+v, want unknown draft with empty operation", draft)
	}
}

func TestIntentCompilerStripsOperationFromUnknownDraftWithoutCatalogValidation(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"unknown","domain":"unknown","operation":"manual_sign.create","confidence":0.23,"slots":[],"reason":"unknown_intent"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "帮张三补签"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActUnknown || draft.Operation != "" || draft.Reason != "unknown_intent" {
		t.Fatalf("draft = %+v, want unknown draft normalized without catalog validation", draft)
	}
}

func TestIntentCompilerReturnsUnknownForDuplicateTopLevelKey(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"read_query","act":"write_request","domain":"attendance","operation":"attendance.query_status","confidence":0.91,"slots":[],"reason":"重复字段"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActUnknown || draft.Reason != "intent_parse_failed" {
		t.Fatalf("draft = %+v, want parse failure for duplicate top-level key", draft)
	}
}

func TestIntentCompilerReturnsUnknownForDuplicateSlotField(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"read_query","domain":"attendance","operation":"attendance.query_status","confidence":0.91,"slots":[{"field":"query_shape","raw":"slot_status"},{"field":"query_shape","raw":"user_day_status"}],"reason":"重复槽位"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActUnknown || draft.Reason != "intent_parse_failed" {
		t.Fatalf("draft = %+v, want parse failure for duplicate slot field", draft)
	}
}

func TestIntentCompilerReturnsUnknownForMissingSlots(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"read_query","domain":"attendance","operation":"attendance.query_status","confidence":0.91,"reason":"缺少 slots"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActUnknown || draft.Reason != "intent_parse_failed" {
		t.Fatalf("draft = %+v, want parse failure for missing slots", draft)
	}
}

func TestIntentCompilerReturnsUnknownForTrustedIDSlots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "user id",
			content: `{"act":"read_query","domain":"schedule","operation":"schedule.query_user_schedule","confidence":0.91,"slots":[{"field":"user_id","raw":"42"}],"reason":"用户查询他人课表"}`,
		},
		{
			name:    "department ids",
			content: `{"act":"write_request","domain":"subscription","operation":"subscription.start","confidence":0.91,"slots":[{"field":"dept_ids","raw":"101"}],"reason":"用户开启部门订阅"}`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeIntentChatClient{content: tt.content}
			compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

			draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "测试"})
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if draft.Act != ActUnknown || draft.Reason != "intent_parse_failed" {
				t.Fatalf("draft = %+v, want parse failure for trusted ID slot", draft)
			}
		})
	}
}

func TestIntentCompilerKeepsLowConfidenceWriteDraft(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"write_request","domain":"subscription","operation":"subscription.start","confidence":0.42,"slots":[{"field":"scope","raw":"本群"}],"reason":"用户可能想开启订阅"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "好像开一下订阅"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActWriteRequest || draft.Operation != "subscription.start" || draft.Confidence != 0.42 {
		t.Fatalf("draft = %+v, want low-confidence write draft preserved", draft)
	}
}

func TestIntentCompilerPromptIncludesCatalogAndUsesNoTools(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"help","domain":"system","operation":"system.describe_capability","confidence":0.88,"slots":[],"reason":"用户询问功能"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	_, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "你有什么功能"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("Chat calls = %d, want 1", client.calls)
	}
	if !client.toolDefsWereNil {
		t.Fatalf("Chat tools = %#v, want nil", client.toolDefs)
	}
	if len(client.messages) < 2 {
		t.Fatalf("messages = %#v, want system and user messages", client.messages)
	}
	if client.messages[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", client.messages[0].Role)
	}
	if client.messages[1].Role != "user" || client.messages[1].Content != "你有什么功能" {
		t.Fatalf("user message = %+v", client.messages[1])
	}

	systemPrompt := client.messages[0].Content
	for _, metadata := range operationCatalogEntries {
		if !strings.Contains(systemPrompt, metadata.Name) {
			t.Fatalf("system prompt does not contain catalog operation %q: %s", metadata.Name, systemPrompt)
		}
	}
	if strings.Contains(systemPrompt, "manual_sign.create") {
		t.Fatalf("system prompt contains operation outside catalog: %s", systemPrompt)
	}
}

func TestPromptOperationEntriesAreDerivedFromOperationCatalog(t *testing.T) {
	t.Parallel()

	entries := promptOperationEntries()
	if len(entries) != len(operationManifests()) {
		t.Fatalf("promptOperationEntries() len = %d, want %d", len(entries), len(operationManifests()))
	}
	for _, entry := range entries {
		manifest, ok := lookupOperation(entry.Name)
		if !ok {
			t.Fatalf("prompt operation %q has no manifest", entry.Name)
		}
		if entry.Domain != manifest.Domain {
			t.Fatalf("%s Domain = %q, want %q", entry.Name, entry.Domain, manifest.Domain)
		}
		if !reflect.DeepEqual(entry.AllowedActs, manifest.AllowedActs) {
			t.Fatalf("%s AllowedActs = %v, want %v", entry.Name, entry.AllowedActs, manifest.AllowedActs)
		}
	}
}

func TestIntentCompilerPromptIncludesActiveWorkflowContext(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"workflow_continue","domain":"subscription","operation":"subscription.start","confidence":0.88,"slots":[{"field":"scope","raw":"全部人员"}],"reason":"用户补充订阅范围"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	draft, err := compiler.Compile(context.Background(), IntentCompileRequest{
		Message: "全部人员",
		ActiveWorkflow: &IntentCompileWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if draft.Act != ActWorkflowContinue || draft.Operation != "subscription.start" {
		t.Fatalf("draft = %+v, want workflow_continue subscription.start", draft)
	}
	if len(client.messages) < 3 {
		t.Fatalf("messages = %#v, want workflow context message", client.messages)
	}
	contextMessage := client.messages[1]
	if contextMessage.Role != "system" || !strings.Contains(contextMessage.Content, "active_workflow_type=subscription.start") ||
		!strings.Contains(contextMessage.Content, "missing_fields=scope") {
		t.Fatalf("workflow context message = %+v", contextMessage)
	}
	if client.messages[2].Role != "user" || client.messages[2].Content != "全部人员" {
		t.Fatalf("user message = %+v", client.messages[2])
	}
}

func TestIntentCompilerPropagatesChatErrors(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("llm unavailable")
	client := &fakeIntentChatClient{err: wantErr}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	_, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "你有什么功能"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Compile() error = %v, want %v", err, wantErr)
	}
}

func TestIntentCompilerUsesConfiguredTimeout(t *testing.T) {
	t.Parallel()

	client := blockingIntentChatClient{}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{Timeout: 10 * time.Millisecond})

	start := time.Now()
	_, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Compile() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Compile() elapsed = %s, want configured timeout to bound the call", elapsed)
	}
}

type fakeIntentChatClient struct {
	content         string
	err             error
	calls           int
	messages        []tools.Message
	toolDefs        []tools.ToolDef
	toolDefsWereNil bool
}

func (c *fakeIntentChatClient) Chat(ctx context.Context, messages []tools.Message, toolDefs []tools.ToolDef) (tools.Message, error) {
	c.calls++
	c.messages = append([]tools.Message(nil), messages...)
	c.toolDefs = append([]tools.ToolDef(nil), toolDefs...)
	c.toolDefsWereNil = toolDefs == nil
	if c.err != nil {
		return tools.Message{}, c.err
	}
	return tools.Message{Role: "assistant", Content: c.content}, nil
}

type blockingIntentChatClient struct{}

func (blockingIntentChatClient) Chat(ctx context.Context, _ []tools.Message, _ []tools.ToolDef) (tools.Message, error) {
	<-ctx.Done()
	return tools.Message{}, ctx.Err()
}
