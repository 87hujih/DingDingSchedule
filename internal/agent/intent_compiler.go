package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"schedule_server/internal/agent/tools"
)

// IntentCompiler compiles a user message into an untrusted protocol draft.
type IntentCompiler interface {
	Compile(ctx context.Context, req IntentCompileRequest) (IntentDraft, error)
}

type IntentCompileRequest struct {
	Message        string
	ActiveWorkflow *IntentCompileWorkflowContext
}

type IntentCompileWorkflowContext struct {
	Type          string
	MissingFields []string
}

type chatClient interface {
	Chat(ctx context.Context, messages []tools.Message, toolDefs []tools.ToolDef) (tools.Message, error)
}

type intentCompilerOptions struct {
	SystemPrompt string
}

type llmIntentCompiler struct {
	client       chatClient
	systemPrompt string
}

type intentCompilerResponse struct {
	Act        UserAct        `json:"act"`
	Domain     BusinessDomain `json:"domain"`
	Operation  string         `json:"operation"`
	Confidence float64        `json:"confidence"`
	Slots      []intentSlot   `json:"slots"`
	Reason     string         `json:"reason"`
}

type intentSlot struct {
	Field string `json:"field"`
	Raw   string `json:"raw"`
}

func newLLMIntentCompiler(client chatClient, opts intentCompilerOptions) IntentCompiler {
	systemPrompt := strings.TrimSpace(opts.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = buildIntentCompilerSystemPrompt()
	}
	return &llmIntentCompiler{
		client:       client,
		systemPrompt: systemPrompt,
	}
}

func (c *llmIntentCompiler) Compile(ctx context.Context, req IntentCompileRequest) (IntentDraft, error) {
	if c.client == nil {
		return IntentDraft{}, errors.New("intent compiler chat client is nil")
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		return unknownIntentDraft("empty_message"), nil
	}

	messages := []tools.Message{
		{Role: "system", Content: c.systemPrompt},
	}
	if req.ActiveWorkflow != nil {
		messages = append(messages, tools.Message{
			Role:    "system",
			Content: buildIntentCompilerWorkflowContext(req.ActiveWorkflow),
		})
	}
	messages = append(messages, tools.Message{Role: "user", Content: message})

	reply, err := c.client.Chat(ctx, messages, nil)
	if err != nil {
		return IntentDraft{}, err
	}

	draft, err := parseIntentCompilerResponse(reply.Content)
	if err != nil {
		return unknownIntentDraft("intent_parse_failed"), nil
	}
	if draft.Operation != "" {
		if _, ok := lookupOperation(draft.Operation); !ok {
			return unknownIntentDraft("operation_not_allowed"), nil
		}
	}
	if draft.Act == ActUnknown {
		draft.Domain = DomainUnknown
		draft.Operation = ""
		return draft, nil
	}
	if draft.Operation == "" {
		return unknownIntentDraft("operation_not_allowed"), nil
	}
	return draft, nil
}

func parseIntentCompilerResponse(content string) (IntentDraft, error) {
	fields, err := decodeUniqueJSONObject(strings.TrimSpace(content))
	if err != nil {
		return IntentDraft{}, err
	}
	allowed := map[string]bool{
		"act":        true,
		"domain":     true,
		"operation":  true,
		"confidence": true,
		"slots":      true,
		"reason":     true,
	}
	for name := range fields {
		if !allowed[name] {
			return IntentDraft{}, fmt.Errorf("unknown field %q", name)
		}
	}

	response := intentCompilerResponse{}
	if err := decodeRequiredJSONField(fields, "act", &response.Act); err != nil {
		return IntentDraft{}, err
	}
	if err := decodeRequiredJSONField(fields, "domain", &response.Domain); err != nil {
		return IntentDraft{}, err
	}
	if err := decodeRequiredJSONField(fields, "operation", &response.Operation); err != nil {
		return IntentDraft{}, err
	}
	if err := decodeRequiredJSONField(fields, "confidence", &response.Confidence); err != nil {
		return IntentDraft{}, err
	}
	response.Slots, err = decodeIntentSlots(fields)
	if err != nil {
		return IntentDraft{}, err
	}
	if err := decodeRequiredJSONField(fields, "reason", &response.Reason); err != nil {
		return IntentDraft{}, err
	}
	if !knownIntentAct(response.Act) || !knownBusinessDomain(response.Domain) {
		return IntentDraft{}, fmt.Errorf("invalid act/domain: %s/%s", response.Act, response.Domain)
	}
	operation := strings.TrimSpace(response.Operation)
	if response.Act != ActUnknown && operation == "" {
		return IntentDraft{}, errors.New("operation is empty")
	}

	slots := make(map[string]SlotDraft, len(response.Slots))
	for _, slot := range response.Slots {
		field := strings.TrimSpace(slot.Field)
		if field == "" {
			return IntentDraft{}, errors.New("slot field is empty")
		}
		if _, exists := slots[field]; exists {
			return IntentDraft{}, fmt.Errorf("duplicate slot field %q", field)
		}
		slots[field] = SlotDraft{Field: field, Raw: slot.Raw}
	}

	return IntentDraft{
		Act:        response.Act,
		Domain:     response.Domain,
		Operation:  operation,
		Confidence: response.Confidence,
		Slots:      slots,
		Reason:     response.Reason,
	}, nil
}

func decodeUniqueJSONObject(content string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(content)))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("expected JSON object")
	}

	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("expected JSON object key")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate field %q", name)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields[name] = raw
	}

	token, err = decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok = token.(json.Delim)
	if !ok || delim != '}' {
		return nil, errors.New("expected JSON object end")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return fields, nil
}

func decodeRequiredJSONField(fields map[string]json.RawMessage, name string, out any) error {
	raw, ok := fields[name]
	if !ok {
		return fmt.Errorf("missing field %q", name)
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return fmt.Errorf("field %q is null", name)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode field %q: %w", name, err)
	}
	return nil
}

func decodeIntentSlots(fields map[string]json.RawMessage) ([]intentSlot, error) {
	raw, ok := fields["slots"]
	if !ok {
		return nil, errors.New("missing field \"slots\"")
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, errors.New("field \"slots\" is null")
	}

	var slotItems []json.RawMessage
	if err := json.Unmarshal(raw, &slotItems); err != nil {
		return nil, fmt.Errorf("decode field \"slots\": %w", err)
	}
	slots := make([]intentSlot, 0, len(slotItems))
	for _, item := range slotItems {
		slotFields, err := decodeUniqueJSONObject(string(item))
		if err != nil {
			return nil, err
		}
		for name := range slotFields {
			if name != "field" && name != "raw" {
				return nil, fmt.Errorf("unknown slot field %q", name)
			}
		}
		var slot intentSlot
		if err := decodeRequiredJSONField(slotFields, "field", &slot.Field); err != nil {
			return nil, err
		}
		if err := decodeRequiredJSONField(slotFields, "raw", &slot.Raw); err != nil {
			return nil, err
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

func buildIntentCompilerSystemPrompt() string {
	var b strings.Builder
	b.WriteString("你是协议意图编译器。只输出一个 JSON 对象，不要输出 Markdown 或解释。\n")
	b.WriteString("输出字段只能是 act, domain, operation, confidence, slots, reason。\n")
	b.WriteString("slots 必须是数组，每项只能包含 field 和 raw；raw 只是用户原文片段，不是可信实体值。\n")
	b.WriteString("只允许使用下面 operation catalog 中的 operation；不确定时输出 act=unknown、domain=unknown、operation 为空字符串。\n")
	b.WriteString("operation catalog:\n")
	for _, metadata := range promptOperationEntries() {
		b.WriteString("- operation=")
		b.WriteString(metadata.Name)
		b.WriteString("; domain=")
		b.WriteString(string(metadata.Domain))
		b.WriteString("; allowed_acts=")
		b.WriteString(strings.Join(userActStrings(metadata.AllowedActs), ","))
		b.WriteString("; is_write=")
		b.WriteString(fmt.Sprintf("%t", metadata.IsWrite))
		if len(metadata.RequiredTrustedParams) > 0 {
			b.WriteString("; required_trusted_params=")
			b.WriteString(strings.Join(metadata.RequiredTrustedParams, ","))
		}
		if len(metadata.OptionalTrustedParams) > 0 {
			b.WriteString("; optional_trusted_params=")
			b.WriteString(strings.Join(metadata.OptionalTrustedParams, ","))
		}
		if len(metadata.QueryShapes) > 0 {
			b.WriteString("; query_shapes=")
			for i, shape := range metadata.QueryShapes {
				if i > 0 {
					b.WriteString("|")
				}
				b.WriteString(shape.Name)
				if len(shape.RequiredTrustedParams) > 0 {
					b.WriteString("(")
					b.WriteString(strings.Join(shape.RequiredTrustedParams, ","))
					b.WriteString(")")
				}
			}
		}
		if len(metadata.Defaults) > 0 {
			b.WriteString("; defaults=")
			fields := make([]string, 0, len(metadata.Defaults))
			for field := range metadata.Defaults {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			for i, field := range fields {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(field)
				b.WriteString(":")
				b.WriteString(string(metadata.Defaults[field]))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func buildIntentCompilerWorkflowContext(workflow *IntentCompileWorkflowContext) string {
	missing := append([]string(nil), workflow.MissingFields...)
	sort.Strings(missing)
	return fmt.Sprintf(
		"当前活跃 workflow 上下文：active_workflow_type=%s; missing_fields=%s。该上下文只能用于判断 workflow_continue/workflow_cancel，不能把 raw slots 当成可信实体。",
		strings.TrimSpace(workflow.Type),
		strings.Join(missing, ","),
	)
}

func userActStrings(acts []UserAct) []string {
	values := make([]string, 0, len(acts))
	for _, act := range acts {
		values = append(values, string(act))
	}
	return values
}

func knownIntentAct(act UserAct) bool {
	switch act {
	case ActCapabilityQuestion, ActRuleQuestion, ActReadQuery, ActWriteRequest,
		ActWorkflowContinue, ActWorkflowCancel, ActHelp, ActUnknown:
		return true
	default:
		return false
	}
}

func knownBusinessDomain(domain BusinessDomain) bool {
	switch domain {
	case DomainSystem, DomainAttendance, DomainSubscription, DomainManualSign,
		DomainSchedule, DomainLeave, DomainAnalytics, DomainUnknown:
		return true
	default:
		return false
	}
}

func unknownIntentDraft(reason string) IntentDraft {
	return IntentDraft{
		Act:           ActUnknown,
		Domain:        DomainUnknown,
		Reason:        reason,
		ClarifyReason: reason,
	}
}
