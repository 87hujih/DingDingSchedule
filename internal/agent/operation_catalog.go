package agent

import (
	"fmt"
	"strings"
)

type SlotDefault string

const (
	SlotDefaultNone        SlotDefault = ""
	SlotDefaultToday       SlotDefault = "today"
	SlotDefaultCurrentWeek SlotDefault = "current_week"
)

type RiskLevel string

const (
	RiskRead        RiskLevel = "read"
	RiskWriteLow    RiskLevel = "write_low"
	RiskWriteMedium RiskLevel = "write_medium"
	RiskWriteHigh   RiskLevel = "write_high"
)

type ConversationScope string

const (
	ConversationScopeBoth  ConversationScope = "both"
	ConversationScopeDM    ConversationScope = "dm"
	ConversationScopeGroup ConversationScope = "group"
)

type WorkflowMode string

const (
	WorkflowModeSingleTurn WorkflowMode = "single_turn"
	WorkflowModeMultiTurn  WorkflowMode = "multi_turn"
	WorkflowModeAuxiliary  WorkflowMode = "auxiliary"
)

type ProtocolLiveDispatchBinding struct {
	Name string
}

type WriteGuardBinding struct {
	Name string
}

const (
	ProtocolLiveDispatchAttendance           = "attendance_request"
	ProtocolLiveDispatchSubscriptionWorkflow = "subscription_workflow"
	ProtocolLiveDispatchRuntimeConversation  = "runtime_conversation"
	ProtocolLiveDispatchCapability           = "capability_answer"
	ProtocolLiveDispatchRuleExplain          = "rule_explain"
	ProtocolLiveDispatchSchedule             = "schedule_request"

	WriteGuardBindingNotRequired = "not_required"
	WriteGuardBindingDefault     = "write_guard"
)

type ParamSpec struct {
	Name string
}

type DefaultSpec struct {
	Param string
	Value SlotDefault
}

type QueryShapeMetadata struct {
	Name                  string
	RequiredTrustedParams []ParamSpec
}

type WorkflowSpec struct {
	Type                  WorkflowType
	Mode                  WorkflowMode
	CollectStates         []WorkflowState
	RequiredTrustedParams []ParamSpec
	AuxiliaryOperations   []string
}

type ResolverSpec struct {
	Param string
	Name  string
}

type PolicySpec struct {
	Name string
}

type IdempotencySpec struct {
	KeyFields []string
}

type ExecutorBinding struct {
	Name string
}

type RendererBinding struct {
	Name string
	Kind ResponseKind
}

type EvalBinding struct {
	CaseIDs    []string
	ReplayTags []string
}

type CapabilityBinding struct {
	Title          string
	Description    string
	AnswerOnly     bool
	DirectlyUsable bool
}

type OperationManifest struct {
	Name        string
	AllowedActs []UserAct
	Domain      BusinessDomain

	IsWrite bool
	Risk    RiskLevel
	Scope   ConversationScope
	MinRole int

	RequiredTrustedParams []ParamSpec
	OptionalTrustedParams []ParamSpec
	QueryShapes           []QueryShapeMetadata
	Defaults              map[string]SlotDefault

	Workflow    *WorkflowSpec
	Dispatch    ProtocolLiveDispatchBinding
	Resolvers   []ResolverSpec
	Policies    []PolicySpec
	Idempotency IdempotencySpec
	WriteGuard  WriteGuardBinding

	Executor   ExecutorBinding
	Renderer   RendererBinding
	Eval       EvalBinding
	Capability *CapabilityBinding
}

// OperationMetadata is kept as a compatibility alias while callers migrate to OperationManifest.
type OperationMetadata = OperationManifest

type OperationPromptEntry struct {
	Name                  string
	Domain                BusinessDomain
	AllowedActs           []UserAct
	IsWrite               bool
	RequiredTrustedParams []string
	OptionalTrustedParams []string
	QueryShapes           []QueryShapePromptEntry
	Defaults              map[string]SlotDefault
}

type QueryShapePromptEntry struct {
	Name                  string
	RequiredTrustedParams []string
}

type EvalOperationEntry struct {
	Name       string
	CaseIDs    []string
	ReplayTags []string
}

var operationCatalogEntries = []OperationManifest{
	{
		Name:                  "attendance.query_status",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainAttendance,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeBoth,
		MinRole:               0,
		RequiredTrustedParams: params("date", "section"),
		OptionalTrustedParams: params("user_id"),
		QueryShapes: []QueryShapeMetadata{
			{
				Name:                  "slot_status",
				RequiredTrustedParams: params("date", "section"),
			},
			{
				Name:                  "user_day_status",
				RequiredTrustedParams: params("date", "user_id"),
			},
		},
		Defaults: map[string]SlotDefault{
			"date": SlotDefaultToday,
		},
		Workflow: singleTurnWorkflowSpec(),
		Resolvers: []ResolverSpec{
			{Param: "date", Name: "date_slot"},
			{Param: "section", Name: "section_slot"},
			{Param: "user_id", Name: "user_resolver"},
			{Param: "week", Name: "semester_default"},
		},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchAttendance},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: "operation_executor"},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseResult},
		Eval:       EvalBinding{CaseIDs: []string{"protocol-attendance-slot-status"}, ReplayTags: []string{"attendance"}},
	},
	{
		Name:                  "subscription.start",
		AllowedActs:           []UserAct{ActWriteRequest},
		Domain:                DomainSubscription,
		IsWrite:               true,
		Risk:                  RiskWriteLow,
		Scope:                 ConversationScopeGroup,
		MinRole:               1,
		RequiredTrustedParams: params("conversation_id", "scope"),
		OptionalTrustedParams: params("dept_ids"),
		Workflow: &WorkflowSpec{
			Type:                  WorkflowSubscriptionStart,
			Mode:                  WorkflowModeMultiTurn,
			CollectStates:         []WorkflowState{WorkflowCollectScope, WorkflowCollectDepartments},
			RequiredTrustedParams: params("scope", "dept_ids"),
			AuxiliaryOperations:   []string{"subscription.list_departments"},
		},
		Resolvers: []ResolverSpec{
			{Param: "conversation_id", Name: "runtime_conversation"},
			{Param: "scope", Name: "subscription_scope"},
			{Param: "dept_ids", Name: "department_resolver"},
		},
		Dispatch: ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchSubscriptionWorkflow},
		Policies: []PolicySpec{{Name: "admin_role"}, {Name: "group_conversation"}, {Name: "subscription_scope"}},
		Idempotency: IdempotencySpec{
			KeyFields: []string{"tenant_id", "conversation_id", "actor_user_id", "operation", "scope", "dept_ids", "workflow_id"},
		},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingDefault},
		Executor:   ExecutorBinding{Name: "operation_executor"},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseResult},
		Eval: EvalBinding{
			CaseIDs:    []string{"protocol-subscription-missing-scope", "protocol-subscription-first-turn-all"},
			ReplayTags: []string{"subscription", "write_low"},
		},
	},
	{
		Name:                  "subscription.cancel",
		AllowedActs:           []UserAct{ActWriteRequest},
		Domain:                DomainSubscription,
		IsWrite:               true,
		Risk:                  RiskWriteLow,
		Scope:                 ConversationScopeGroup,
		MinRole:               1,
		RequiredTrustedParams: params("conversation_id"),
		Workflow: &WorkflowSpec{
			Type:                  WorkflowType("subscription.cancel"),
			Mode:                  WorkflowModeSingleTurn,
			RequiredTrustedParams: params("conversation_id"),
		},
		Resolvers: []ResolverSpec{{Param: "conversation_id", Name: "runtime_conversation"}},
		Dispatch:  ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuntimeConversation},
		Policies:  []PolicySpec{{Name: "admin_role"}, {Name: "group_conversation"}},
		Idempotency: IdempotencySpec{
			KeyFields: []string{"tenant_id", "conversation_id", "actor_user_id", "operation"},
		},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingDefault},
		Executor:   ExecutorBinding{Name: "operation_executor"},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseResult},
		Eval:       EvalBinding{CaseIDs: []string{"catalog-subscription-cancel"}, ReplayTags: []string{"subscription", "write_low"}},
	},
	{
		Name:                  "subscription.query_status",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainSubscription,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeGroup,
		MinRole:               0,
		RequiredTrustedParams: params("conversation_id"),
		Workflow:              singleTurnWorkflowSpec(),
		Resolvers:             []ResolverSpec{{Param: "conversation_id", Name: "runtime_conversation"}},
		Dispatch:              ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuntimeConversation},
		Policies:              []PolicySpec{{Name: "group_conversation"}},
		WriteGuard:            WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:              ExecutorBinding{Name: "operation_executor"},
		Renderer:              RendererBinding{Name: "response_renderer", Kind: ResponseResult},
		Eval:                  EvalBinding{CaseIDs: []string{"catalog-subscription-query-status"}, ReplayTags: []string{"subscription"}},
	},
	{
		Name:        "subscription.list_departments",
		AllowedActs: []UserAct{ActReadQuery, ActWorkflowContinue},
		Domain:      DomainSubscription,
		Risk:        RiskRead,
		Scope:       ConversationScopeBoth,
		MinRole:     0,
		Workflow: &WorkflowSpec{
			Type:                WorkflowSubscriptionStart,
			Mode:                WorkflowModeAuxiliary,
			AuxiliaryOperations: []string{"subscription.start"},
		},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchSubscriptionWorkflow},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: "operation_executor"},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseSelectOptions},
		Eval:       EvalBinding{CaseIDs: []string{"protocol-subscription-list-departments-workflow-meta"}, ReplayTags: []string{"subscription", "workflow"}},
	},
	{
		Name:        "system.describe_capability",
		AllowedActs: []UserAct{ActHelp},
		Domain:      DomainSystem,
		Risk:        RiskRead,
		Scope:       ConversationScopeBoth,
		MinRole:     0,
		Workflow:    singleTurnWorkflowSpec(),
		Dispatch:    ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:    []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard:  WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:    ExecutorBinding{Name: "operation_executor"},
		Renderer:    RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:        EvalBinding{CaseIDs: []string{"protocol-help-overview"}, ReplayTags: []string{"capability"}},
		Capability: &CapabilityBinding{
			Title:          "功能说明",
			Description:    "说明我当前可以处理的考勤、订阅、规则和课表能力。",
			AnswerOnly:     true,
			DirectlyUsable: true,
		},
	},
	{
		Name:        "attendance.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainAttendance,
		Risk:        RiskRead,
		Scope:       ConversationScopeBoth,
		MinRole:     0,
		Workflow:    singleTurnWorkflowSpec(),
		Dispatch:    ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:    []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard:  WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:    ExecutorBinding{Name: "operation_executor"},
		Renderer:    RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:        EvalBinding{CaseIDs: []string{"catalog-attendance-describe-capability"}, ReplayTags: []string{"capability", "attendance"}},
		Capability: &CapabilityBinding{
			Title:          "考勤查询",
			Description:    "查询指定日期和节次的考勤状态。",
			AnswerOnly:     true,
			DirectlyUsable: true,
		},
	},
	{
		Name:        "schedule.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainSchedule,
		Risk:        RiskRead,
		Scope:       ConversationScopeBoth,
		MinRole:     0,
		Workflow:    singleTurnWorkflowSpec(),
		Dispatch:    ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:    []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard:  WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:    ExecutorBinding{Name: "operation_executor"},
		Renderer:    RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:        EvalBinding{CaseIDs: []string{"catalog-schedule-describe-capability"}, ReplayTags: []string{"capability", "schedule"}},
		Capability: &CapabilityBinding{
			Title:          "课表查询",
			Description:    "查询自己的课表，也可以查询指定姓名用户的课表。",
			AnswerOnly:     true,
			DirectlyUsable: true,
		},
	},
	{
		Name:        "subscription.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainSubscription,
		Risk:        RiskRead,
		Scope:       ConversationScopeGroup,
		MinRole:     0,
		Workflow:    singleTurnWorkflowSpec(),
		Dispatch:    ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:    []PolicySpec{{Name: "group_conversation"}},
		WriteGuard:  WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:    ExecutorBinding{Name: "operation_executor"},
		Renderer:    RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:        EvalBinding{CaseIDs: []string{"catalog-subscription-describe-capability"}, ReplayTags: []string{"capability", "subscription"}},
		Capability: &CapabilityBinding{
			Title:          "群考勤订阅",
			Description:    "在群聊里可以查询当前群考勤推送订阅状态；管理员还可以开启、取消或按部门管理订阅。",
			AnswerOnly:     true,
			DirectlyUsable: true,
		},
	},
	{
		Name:        "manual_sign.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainManualSign,
		Risk:        RiskRead,
		Scope:       ConversationScopeBoth,
		MinRole:     0,
		Workflow:    singleTurnWorkflowSpec(),
		Dispatch:    ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:    []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard:  WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:    ExecutorBinding{Name: "operation_executor"},
		Renderer:    RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval: EvalBinding{
			CaseIDs:    []string{"protocol-manual-sign-capability", "protocol-workflow-interrupted-by-capability"},
			ReplayTags: []string{"capability", "manual_sign"},
		},
		Capability: &CapabilityBinding{
			Title:          "管理员补签",
			Description:    "说明代签/补签能力和所需信息；当前聊天路径不直接执行补签。",
			AnswerOnly:     true,
			DirectlyUsable: false,
		},
	},
	{
		Name:                  "attendance.rule_explain",
		AllowedActs:           []UserAct{ActRuleQuestion},
		Domain:                DomainAttendance,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeBoth,
		MinRole:               0,
		RequiredTrustedParams: params("rule_topic"),
		Workflow:              singleTurnWorkflowSpec(),
		Resolvers:             []ResolverSpec{{Param: "rule_topic", Name: "rule_topic_slot"}},
		Dispatch:              ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuleExplain},
		Policies:              []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard:            WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:              ExecutorBinding{Name: "operation_executor"},
		Renderer:              RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:                  EvalBinding{CaseIDs: []string{"protocol-rule-no-hit"}, ReplayTags: []string{"rule", "attendance"}},
	},
	{
		Name:                  "schedule.rule_explain",
		AllowedActs:           []UserAct{ActRuleQuestion},
		Domain:                DomainSchedule,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeBoth,
		MinRole:               0,
		RequiredTrustedParams: params("rule_topic"),
		Workflow:              singleTurnWorkflowSpec(),
		Resolvers:             []ResolverSpec{{Param: "rule_topic", Name: "rule_topic_slot"}},
		Dispatch:              ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuleExplain},
		Policies:              []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard:            WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:              ExecutorBinding{Name: "operation_executor"},
		Renderer:              RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:                  EvalBinding{CaseIDs: []string{"catalog-schedule-rule-explain"}, ReplayTags: []string{"rule", "schedule"}},
	},
	{
		Name:                  "subscription.rule_explain",
		AllowedActs:           []UserAct{ActRuleQuestion},
		Domain:                DomainSubscription,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeBoth,
		MinRole:               0,
		RequiredTrustedParams: params("rule_topic"),
		Workflow:              singleTurnWorkflowSpec(),
		Resolvers:             []ResolverSpec{{Param: "rule_topic", Name: "rule_topic_slot"}},
		Dispatch:              ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuleExplain},
		Policies:              []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard:            WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:              ExecutorBinding{Name: "operation_executor"},
		Renderer:              RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:                  EvalBinding{CaseIDs: []string{"catalog-subscription-rule-explain"}, ReplayTags: []string{"rule", "subscription"}},
	},
	{
		Name:                  "schedule.query_my_schedule",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainSchedule,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeBoth,
		MinRole:               0,
		RequiredTrustedParams: params("week"),
		Defaults: map[string]SlotDefault{
			"week": SlotDefaultCurrentWeek,
		},
		Workflow:   singleTurnWorkflowSpec(),
		Resolvers:  []ResolverSpec{{Param: "week", Name: "week_slot"}},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchSchedule},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: "operation_executor"},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseResult},
		Eval:       EvalBinding{CaseIDs: []string{"protocol-my-schedule"}, ReplayTags: []string{"schedule"}},
	},
	{
		Name:                  "schedule.query_user_schedule",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainSchedule,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeBoth,
		MinRole:               0,
		RequiredTrustedParams: params("user_id", "week"),
		Defaults: map[string]SlotDefault{
			"week": SlotDefaultCurrentWeek,
		},
		Workflow:   singleTurnWorkflowSpec(),
		Resolvers:  []ResolverSpec{{Param: "user_id", Name: "user_resolver"}, {Param: "week", Name: "week_slot"}},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchSchedule},
		Policies:   []PolicySpec{{Name: "conversation_scope"}, {Name: "schedule_user_visibility"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: "operation_executor"},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseResult},
		Eval:       EvalBinding{CaseIDs: []string{"catalog-schedule-query-user-schedule"}, ReplayTags: []string{"schedule"}},
	},
}

func params(names ...string) []ParamSpec {
	specs := make([]ParamSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, ParamSpec{Name: name})
	}
	return specs
}

func singleTurnWorkflowSpec() *WorkflowSpec {
	return &WorkflowSpec{Mode: WorkflowModeSingleTurn}
}

func paramNames(specs []ParamSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec.Name == "" {
			continue
		}
		names = append(names, spec.Name)
	}
	return names
}

func operationManifests() []OperationManifest {
	manifests := make([]OperationManifest, len(operationCatalogEntries))
	copy(manifests, operationCatalogEntries)
	return manifests
}

// operationNames returns the catalog operation names in whitelist order.
func operationNames() []string {
	names := make([]string, 0, len(operationCatalogEntries))
	for _, manifest := range operationCatalogEntries {
		names = append(names, manifest.Name)
	}
	return names
}

// lookupOperation looks up operation.
func lookupOperation(name string) (OperationManifest, bool) {
	for _, manifest := range operationCatalogEntries {
		if manifest.Name == name {
			return manifest, true
		}
	}
	return OperationManifest{}, false
}

func operationNameForActDomain(act UserAct, domain BusinessDomain) (string, bool) {
	for _, manifest := range operationCatalogEntries {
		if manifest.Domain == domain && actAllowed(act, manifest.AllowedActs) {
			return manifest.Name, true
		}
	}
	return "", false
}

func promptOperationEntries() []OperationPromptEntry {
	entries := make([]OperationPromptEntry, 0, len(operationCatalogEntries))
	for _, manifest := range operationCatalogEntries {
		entry := OperationPromptEntry{
			Name:                  manifest.Name,
			Domain:                manifest.Domain,
			AllowedActs:           append([]UserAct(nil), manifest.AllowedActs...),
			IsWrite:               manifest.IsWrite,
			RequiredTrustedParams: paramNames(manifest.RequiredTrustedParams),
			OptionalTrustedParams: paramNames(manifest.OptionalTrustedParams),
			Defaults:              cloneSlotDefaults(manifest.Defaults),
		}
		if len(manifest.QueryShapes) > 0 {
			entry.QueryShapes = make([]QueryShapePromptEntry, 0, len(manifest.QueryShapes))
			for _, shape := range manifest.QueryShapes {
				entry.QueryShapes = append(entry.QueryShapes, QueryShapePromptEntry{
					Name:                  shape.Name,
					RequiredTrustedParams: paramNames(shape.RequiredTrustedParams),
				})
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func evalOperationEntries() []EvalOperationEntry {
	entries := make([]EvalOperationEntry, 0, len(operationCatalogEntries))
	for _, manifest := range operationCatalogEntries {
		entries = append(entries, EvalOperationEntry{
			Name:       manifest.Name,
			CaseIDs:    append([]string(nil), manifest.Eval.CaseIDs...),
			ReplayTags: append([]string(nil), manifest.Eval.ReplayTags...),
		})
	}
	return entries
}

func cloneSlotDefaults(defaults map[string]SlotDefault) map[string]SlotDefault {
	if len(defaults) == 0 {
		return nil
	}
	cloned := make(map[string]SlotDefault, len(defaults))
	for key, value := range defaults {
		cloned[key] = value
	}
	return cloned
}

func lintOperationCatalog(entries []OperationManifest) []string {
	var errs []string
	seen := make(map[string]struct{}, len(entries))
	for i, manifest := range entries {
		prefix := fmt.Sprintf("operation[%d]", i)
		if strings.TrimSpace(manifest.Name) == "" {
			errs = append(errs, prefix+": name is required")
			continue
		}
		prefix = manifest.Name
		if _, exists := seen[manifest.Name]; exists {
			errs = append(errs, prefix+": duplicate name")
		}
		seen[manifest.Name] = struct{}{}
		if manifest.Domain == "" || manifest.Domain == DomainUnknown {
			errs = append(errs, prefix+": domain is required")
		}
		if len(manifest.AllowedActs) == 0 {
			errs = append(errs, prefix+": allowed acts are required")
		}
		if manifest.Risk == "" {
			errs = append(errs, prefix+": risk is required")
		}
		if manifest.Scope == "" {
			errs = append(errs, prefix+": conversation scope is required")
		}
		if manifest.Workflow == nil {
			errs = append(errs, prefix+": workflow binding is required")
		} else if manifest.Workflow.Mode == "" {
			errs = append(errs, prefix+": workflow mode is required")
		}
		if manifest.Dispatch.Name == "" {
			errs = append(errs, prefix+": protocol_live dispatch binding is required")
		} else if _, ok := lookupProtocolLiveDispatch(manifest.Dispatch.Name); !ok {
			errs = append(errs, prefix+": protocol_live dispatch binding is unknown")
		}
		if manifest.WriteGuard.Name == "" {
			errs = append(errs, prefix+": write guard binding is required")
		}
		if manifest.Executor.Name == "" {
			errs = append(errs, prefix+": executor binding is required")
		}
		if manifest.Renderer.Name == "" {
			errs = append(errs, prefix+": renderer binding is required")
		}
		if len(manifest.Eval.CaseIDs) == 0 {
			errs = append(errs, prefix+": eval binding is required")
		}
		if manifest.IsWrite {
			errs = append(errs, lintWriteManifest(manifest)...)
		} else if manifest.WriteGuard.Name != WriteGuardBindingNotRequired {
			errs = append(errs, prefix+": read operation write guard must be not_required")
		}
		errs = append(errs, lintParamResolution(manifest, manifest.RequiredTrustedParams)...)
		for _, shape := range manifest.QueryShapes {
			errs = append(errs, lintParamResolution(manifest, shape.RequiredTrustedParams)...)
		}
	}
	return errs
}

func lintWriteManifest(manifest OperationManifest) []string {
	var errs []string
	prefix := manifest.Name
	if manifest.Risk == RiskRead {
		errs = append(errs, prefix+": write operation cannot use read risk")
	}
	if manifest.WriteGuard.Name != WriteGuardBindingDefault {
		errs = append(errs, prefix+": write operation must bind write_guard")
	}
	if manifest.Workflow == nil {
		errs = append(errs, prefix+": write workflow is required")
	}
	if len(manifest.Policies) == 0 {
		errs = append(errs, prefix+": write policies are required")
	}
	if len(manifest.Idempotency.KeyFields) == 0 {
		errs = append(errs, prefix+": write idempotency key fields are required")
	}
	return errs
}

func lintParamResolution(manifest OperationManifest, params []ParamSpec) []string {
	var errs []string
	for _, param := range params {
		if param.Name == "" {
			errs = append(errs, manifest.Name+": required param name is empty")
			continue
		}
		if paramResolvable(manifest, param.Name) {
			continue
		}
		errs = append(errs, fmt.Sprintf("%s: required param %q has no resolver, default, or workflow source", manifest.Name, param.Name))
	}
	return errs
}

func paramResolvable(manifest OperationManifest, name string) bool {
	if manifest.Defaults != nil {
		if _, ok := manifest.Defaults[name]; ok {
			return true
		}
	}
	for _, resolver := range manifest.Resolvers {
		if resolver.Param == name && resolver.Name != "" {
			return true
		}
	}
	if manifest.Workflow != nil {
		for _, param := range manifest.Workflow.RequiredTrustedParams {
			if param.Name == name {
				return true
			}
		}
	}
	return false
}
