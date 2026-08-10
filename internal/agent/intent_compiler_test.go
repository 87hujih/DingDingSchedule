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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "帮张三补签"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "最近怎么样"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "随便"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "帮张三补签"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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

			result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "测试"})
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			draft := result.Draft
			if draft.Act != ActUnknown || draft.Reason != "intent_parse_failed" {
				t.Fatalf("draft = %+v, want parse failure for trusted ID slot", draft)
			}
		})
	}
}

func TestIntentCompilerReturnsUnknownForUndeclaredCatalogSlot(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"help","domain":"system","operation":"system.describe_capability","confidence":0.9,"slots":[{"field":"topic","raw":"课表"}],"reason":"用户询问功能"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "你能做什么"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Status != IntentCompileInvalidOutput || result.Draft.Reason != "intent_parse_failed" {
		t.Fatalf("result = %+v, want invalid output for undeclared catalog slot", result)
	}
}

func TestIntentCompilerRejectsConflictingRawAliasesForSameTrustedParam(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"write_request","domain":"subscription","operation":"subscription.start","confidence":0.95,"slots":[{"field":"scope","raw":"指定部门"},{"field":"dept_names","raw":"信工25级"},{"field":"department","raw":"信工24级"}],"reason":"用户开启部门订阅"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "开启部门订阅"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Status != IntentCompileInvalidOutput || result.Draft.Act != ActUnknown {
		t.Fatalf("result = %+v, want conflicting raw aliases to fail closed", result)
	}
}

func TestIntentCompilerAcceptsLegacyRawAliasWhenUnambiguous(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"read_query","domain":"schedule","operation":"schedule.query_user_schedule","confidence":0.95,"slots":[{"field":"user","raw":"杨思见"}],"reason":"用户查询他人课表"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询杨思见的课表"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Status != IntentCompileOK || draftSlotRaw(result.Draft, "user") != "杨思见" {
		t.Fatalf("result = %+v, want unambiguous legacy raw alias accepted", result)
	}
}

func TestIntentCompilerKeepsLowConfidenceWriteDraft(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"write_request","domain":"subscription","operation":"subscription.start","confidence":0.42,"slots":[{"field":"scope","raw":"本群"}],"reason":"用户可能想开启订阅"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "好像开一下订阅"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
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
	if client.messages[1].Role != "user" || !strings.Contains(client.messages[1].Content, `"current_message":"你有什么功能"`) {
		t.Fatalf("user message = %+v", client.messages[1])
	}

	systemPrompt := client.messages[0].Content
	for _, metadata := range operationCatalogEntries {
		if !strings.Contains(systemPrompt, metadata.Name) {
			t.Fatalf("system prompt does not contain catalog operation %q: %s", metadata.Name, systemPrompt)
		}
	}
	for _, fragment := range []string{
		"aliases=开启考勤订阅",
		"取消考勤推送",
		"当前都有哪些部门",
	} {
		if !strings.Contains(systemPrompt, fragment) {
			t.Fatalf("system prompt does not contain catalog recognition fragment %q: %s", fragment, systemPrompt)
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

func TestIntentCompilerPromptIncludesLanguageAndRawSlotContract(t *testing.T) {
	t.Parallel()

	prompt := buildIntentCompilerSystemPrompt()
	for _, fragment := range []string{
		"根据整句话的语义目标分类",
		"不要求用户出现相同关键词",
		"查询指定用户在指定教学周的课程安排",
		"user_name->user_id(user_resolver)",
		"查询一下杨思见的课程信息",
		"negative_examples=查我的课表",
	} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("system prompt missing %q: %s", fragment, prompt)
		}
	}
}

func TestIntentCompilerUsesSemanticTimeoutBudgetByDefault(t *testing.T) {
	t.Parallel()

	compiler, ok := newLLMIntentCompiler(&fakeIntentChatClient{}, intentCompilerOptions{}).(*llmIntentCompiler)
	if !ok {
		t.Fatalf("compiler type = %T, want *llmIntentCompiler", compiler)
	}
	if compiler.timeout != 12*time.Second {
		t.Fatalf("timeout = %s, want 12s", compiler.timeout)
	}
}

func TestIntentCompilerPromptIncludesActiveWorkflowContext(t *testing.T) {
	t.Parallel()

	client := &fakeIntentChatClient{
		content: `{"act":"workflow_continue","domain":"subscription","operation":"subscription.start","confidence":0.88,"slots":[{"field":"scope","raw":"全部人员"}],"reason":"用户补充订阅范围"}`,
	}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{
		Message: "全部人员",
		ActiveWorkflow: &IntentCompileWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	draft := result.Draft
	if draft.Act != ActWorkflowContinue || draft.Operation != "subscription.start" {
		t.Fatalf("draft = %+v, want workflow_continue subscription.start", draft)
	}
	if len(client.messages) != 2 {
		t.Fatalf("messages = %#v, want system and user envelope", client.messages)
	}
	contextMessage := client.messages[1]
	if contextMessage.Role != "user" || !strings.Contains(contextMessage.Content, `"type":"subscription.start"`) ||
		!strings.Contains(contextMessage.Content, `"missing_fields":["scope"]`) {
		t.Fatalf("workflow context message = %+v", contextMessage)
	}
	if !strings.Contains(contextMessage.Content, `"current_message":"全部人员"`) {
		t.Fatalf("user message = %+v", contextMessage)
	}
}

func TestIntentCompilerPropagatesChatErrors(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("llm unavailable")
	client := &fakeIntentChatClient{err: wantErr}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{})

	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "你有什么功能"})
	if err != nil {
		t.Fatalf("Compile() error = %v, want typed transport result", err)
	}
	if result.Status != IntentCompileTransportError {
		t.Fatalf("Status = %q, want %q", result.Status, IntentCompileTransportError)
	}
}

func TestIntentCompilerUsesConfiguredTimeout(t *testing.T) {
	t.Parallel()

	client := blockingIntentChatClient{}
	compiler := newLLMIntentCompiler(client, intentCompilerOptions{Timeout: 10 * time.Millisecond})

	start := time.Now()
	result, err := compiler.Compile(context.Background(), IntentCompileRequest{Message: "查询今天第二节考勤"})
	if err != nil {
		t.Fatalf("Compile() error = %v, want typed child timeout", err)
	}
	if result.Status != IntentCompileTimeout {
		t.Fatalf("Status = %q, want %q", result.Status, IntentCompileTimeout)
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

func (c *fakeIntentChatClient) ChatStructured(
	ctx context.Context,
	messages []tools.Message,
	_ StructuredOutputSpec,
) (StructuredChatResponse, error) {
	c.calls++
	c.messages = append([]tools.Message(nil), messages...)
	c.toolDefs = nil
	c.toolDefsWereNil = true
	if c.err != nil {
		return StructuredChatResponse{Attempts: 1}, c.err
	}
	return StructuredChatResponse{
		Message:  tools.Message{Role: "assistant", Content: c.content},
		Attempts: 1,
	}, nil
}

type blockingIntentChatClient struct{}

func (blockingIntentChatClient) ChatStructured(
	ctx context.Context,
	_ []tools.Message,
	_ StructuredOutputSpec,
) (StructuredChatResponse, error) {
	<-ctx.Done()
	return StructuredChatResponse{}, ctx.Err()
}
