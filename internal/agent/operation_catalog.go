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

type IdempotencyGuarantee string

const (
	IdempotencyGuaranteeRepositoryUniqueUpsert IdempotencyGuarantee = "repository_unique_upsert"
	IdempotencyGuaranteeRepositorySoftDelete   IdempotencyGuarantee = "repository_soft_delete"
)

type IdempotencySpec struct {
	KeyFields []string
	Guarantee IdempotencyGuarantee
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
	Recognition           RecognitionSpec

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
	Description           string
	RequiredTrustedParams []string
	OptionalTrustedParams []string
	QueryShapes           []QueryShapePromptEntry
	Defaults              map[string]SlotDefault
	Aliases               []string
	Examples              []string
	NegativeExamples      []string
	RawSlots              []RawSlotSpec
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
		OptionalTrustedParams: params("user_id", "query_shape"),
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
		Recognition: RecognitionSpec{
			Description: "查询指定日期和节次的考勤状态，或查询指定用户某天的考勤状态",
			Examples: []string{
				"查询今天第二节考勤状态",
				"张三今天考勤怎么样",
			},
			NegativeExamples: []string{"考勤迟到规则是什么", "开启本群考勤订阅"},
			RawSlots: []RawSlotSpec{
				{RawName: "date", TargetParam: "date", Resolver: "date_slot"},
				{RawName: "section", TargetParam: "section", Resolver: "section_slot"},
				{RawName: "user_name", TargetParam: "user_id", Resolver: "user_resolver"},
				{RawName: "user", TargetParam: "user_id", Resolver: "user_resolver"},
				{RawName: "query_shape", TargetParam: "query_shape", Resolver: "query_shape_slot"},
			},
		},
		Workflow: singleTurnWorkflowSpec(),
		Resolvers: []ResolverSpec{
			{Param: "date", Name: "date_slot"},
			{Param: "section", Name: "section_slot"},
			{Param: "user_id", Name: "user_resolver"},
			{Param: "week", Name: "semester_default"},
			{Param: "query_shape", Name: "query_shape_slot"},
		},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchAttendance},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingAttendanceQueryStatus},
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
		Recognition: RecognitionSpec{
			Description: "在当前群开启考勤推送订阅；这是写操作，需要管理员权限，并可能继续询问订阅范围或部门",
			Aliases: []string{
				"开启考勤订阅",
				"开启本群考勤订阅",
				"打开本群考勤推送",
				"开通考勤订阅",
			},
			Examples:         []string{"开启本群全部人员考勤订阅", "给本群开启信工25级考勤推送"},
			NegativeExamples: []string{"本群订阅开了吗", "关闭本群考勤推送"},
			RawSlots: []RawSlotSpec{
				{RawName: "scope", TargetParam: "scope", Resolver: "subscription_scope", Shape: "subscription_scope"},
				{RawName: "dept_names", TargetParam: "dept_ids", Resolver: "department_resolver", Shape: "department_name_or_candidate"},
				{RawName: "dept_name", TargetParam: "dept_ids", Resolver: "department_resolver", Shape: "department_name_or_candidate"},
				{RawName: "department", TargetParam: "dept_ids", Resolver: "department_resolver", Shape: "department_name_or_candidate"},
				{RawName: "department_name", TargetParam: "dept_ids", Resolver: "department_resolver", Shape: "department_name_or_candidate"},
			},
		},
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
			Guarantee: IdempotencyGuaranteeRepositoryUniqueUpsert,
		},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingDefault},
		Executor:   ExecutorBinding{Name: ExecutorBindingSubscriptionStart},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseResult},
		Eval: EvalBinding{
			CaseIDs:    []string{"protocol-subscription-missing-scope", "protocol-subscription-first-turn-all", "protocol-workflow-cancel-active-subscription"},
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
		Recognition: RecognitionSpec{
			Description: "取消当前群已经开启的考勤推送订阅；这是需要管理员权限的写操作",
			Aliases: []string{
				"取消考勤推送",
				"取消本群考勤推送",
				"取消考勤订阅",
				"关闭考勤订阅",
				"关闭本群考勤推送",
			},
			Examples:         []string{"关闭这个群的考勤推送", "取消本群考勤订阅"},
			NegativeExamples: []string{"本群订阅状态是什么", "开启本群考勤订阅"},
		},
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
			Guarantee: IdempotencyGuaranteeRepositorySoftDelete,
		},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingDefault},
		Executor:   ExecutorBinding{Name: ExecutorBindingSubscriptionCancel},
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
		Recognition: RecognitionSpec{
			Description: "查询当前群是否已开启考勤推送订阅以及订阅范围",
			Aliases: []string{
				"查本群订阅状态",
				"当前群有没有订阅",
				"有没有开启考勤订阅",
				"查这个群有没有开启考勤订阅",
				"订阅状态",
				"有没有订阅",
			},
			Examples:         []string{"这个群考勤订阅开了没", "查一下本群订阅状态"},
			NegativeExamples: []string{"开启本群考勤订阅", "群考勤订阅规则是什么"},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Resolvers:  []ResolverSpec{{Param: "conversation_id", Name: "runtime_conversation"}},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuntimeConversation},
		Policies:   []PolicySpec{{Name: "group_conversation"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingSubscriptionQueryStatus},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseResult},
		Eval:       EvalBinding{CaseIDs: []string{"catalog-subscription-query-status"}, ReplayTags: []string{"subscription"}},
	},
	{
		Name:        "subscription.list_departments",
		AllowedActs: []UserAct{ActReadQuery, ActWorkflowContinue},
		Domain:      DomainSubscription,
		Risk:        RiskRead,
		Scope:       ConversationScopeBoth,
		MinRole:     0,
		Recognition: RecognitionSpec{
			Description: "列出群考勤订阅流程中可以选择的部门，不代表执行开启订阅",
			Aliases: []string{
				"当前都有哪些部门",
				"都有哪些部门",
				"有哪些部门",
				"部门列表",
				"部门有哪些",
				"哪些部门",
			},
			Examples:         []string{"先看看有哪些部门", "显示部门列表"},
			NegativeExamples: []string{"订阅信工25级", "开启全部人员订阅"},
			ContinueShapes: []ContinueShape{
				{WorkflowType: WorkflowSubscriptionStart, States: []WorkflowState{WorkflowCollectScope, WorkflowCollectDepartments}, Source: "auxiliary_operation"},
			},
		},
		Workflow: &WorkflowSpec{
			Type:                WorkflowSubscriptionStart,
			Mode:                WorkflowModeAuxiliary,
			AuxiliaryOperations: []string{"subscription.start"},
		},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchSubscriptionWorkflow},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingSubscriptionListDepartments},
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
		Recognition: RecognitionSpec{
			Description:      "概览智能助手当前支持的全部功能",
			Examples:         []string{"你有什么功能", "你能帮我做什么"},
			NegativeExamples: []string{"考勤能做什么", "查询我的课表"},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingSystemDescribeCapability},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:       EvalBinding{CaseIDs: []string{"protocol-help-overview"}, ReplayTags: []string{"capability"}},
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
		Recognition: RecognitionSpec{
			Description:      "说明考勤查询能力，不执行具体考勤查询",
			Examples:         []string{"考勤能做什么", "支持哪些考勤查询"},
			NegativeExamples: []string{"查询今天第二节考勤状态", "迟到规则是什么"},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingAttendanceDescribeCapability},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:       EvalBinding{CaseIDs: []string{"catalog-attendance-describe-capability"}, ReplayTags: []string{"capability", "attendance"}},
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
		Recognition: RecognitionSpec{
			Description:      "说明课表查询能力和可查询对象，不执行具体课表查询",
			Examples:         []string{"课表能查什么", "可以查别人的课程吗"},
			NegativeExamples: []string{"查询杨思见的课程信息", "课表规则是什么"},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingScheduleDescribeCapability},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:       EvalBinding{CaseIDs: []string{"catalog-schedule-describe-capability"}, ReplayTags: []string{"capability", "schedule"}},
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
		Recognition: RecognitionSpec{
			Description:      "说明群考勤订阅支持的查询、开启、取消和按部门管理能力，不执行写操作",
			Examples:         []string{"考勤订阅能做什么", "群推送支持哪些功能"},
			NegativeExamples: []string{"开启本群考勤订阅", "本群订阅开了吗"},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:   []PolicySpec{{Name: "group_conversation"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingSubscriptionDescribeCapability},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:       EvalBinding{CaseIDs: []string{"catalog-subscription-describe-capability"}, ReplayTags: []string{"capability", "subscription"}},
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
		Recognition: RecognitionSpec{
			Description:      "说明补签或代签所需信息和当前聊天能力边界；当前聊天不能直接执行补签",
			Examples:         []string{"可以补签吗", "补签支持什么流程"},
			NegativeExamples: []string{"给张三补签今天第一节", "查询张三今天考勤"},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchCapability},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingManualSignDescribeCapability},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
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
		Recognition: RecognitionSpec{
			Description:      "解释考勤、迟到、缺勤、打卡或休息日相关规则，不查询实时考勤结果",
			Examples:         []string{"迟到规则是什么", "休息日和考勤冲突怎么判"},
			NegativeExamples: []string{"查询今天第二节考勤状态", "考勤能做什么"},
			RawSlots:         []RawSlotSpec{{RawName: "rule_topic", TargetParam: "rule_topic", Resolver: "rule_topic_slot"}},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Resolvers:  []ResolverSpec{{Param: "rule_topic", Name: "rule_topic_slot"}},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuleExplain},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingAttendanceRuleExplain},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:       EvalBinding{CaseIDs: []string{"protocol-rule-no-hit"}, ReplayTags: []string{"rule", "attendance"}},
	},
	{
		Name:                  "schedule.rule_explain",
		AllowedActs:           []UserAct{ActRuleQuestion},
		Domain:                DomainSchedule,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeBoth,
		MinRole:               0,
		RequiredTrustedParams: params("rule_topic"),
		Recognition: RecognitionSpec{
			Description:      "解释课表、排课或课程可见性规则，不查询具体课表",
			Examples:         []string{"课表规则是什么", "排课规则按什么口径生效"},
			NegativeExamples: []string{"查询杨思见的课程信息", "课表能查什么"},
			RawSlots:         []RawSlotSpec{{RawName: "rule_topic", TargetParam: "rule_topic", Resolver: "rule_topic_slot"}},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Resolvers:  []ResolverSpec{{Param: "rule_topic", Name: "rule_topic_slot"}},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuleExplain},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingScheduleRuleExplain},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:       EvalBinding{CaseIDs: []string{"catalog-schedule-rule-explain"}, ReplayTags: []string{"rule", "schedule"}},
	},
	{
		Name:                  "subscription.rule_explain",
		AllowedActs:           []UserAct{ActRuleQuestion},
		Domain:                DomainSubscription,
		Risk:                  RiskRead,
		Scope:                 ConversationScopeBoth,
		MinRole:               0,
		RequiredTrustedParams: params("rule_topic"),
		Recognition: RecognitionSpec{
			Description:      "解释群考勤订阅、推送范围和部门订阅规则，不查询或修改当前订阅",
			Examples:         []string{"群订阅规则是什么", "按部门订阅如何生效"},
			NegativeExamples: []string{"本群订阅开了吗", "关闭本群考勤推送"},
			RawSlots:         []RawSlotSpec{{RawName: "rule_topic", TargetParam: "rule_topic", Resolver: "rule_topic_slot"}},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Resolvers:  []ResolverSpec{{Param: "rule_topic", Name: "rule_topic_slot"}},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchRuleExplain},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingSubscriptionRuleExplain},
		Renderer:   RendererBinding{Name: "response_renderer", Kind: ResponseAnswer},
		Eval:       EvalBinding{CaseIDs: []string{"catalog-subscription-rule-explain"}, ReplayTags: []string{"rule", "subscription"}},
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
		Recognition: RecognitionSpec{
			Description:      "查询当前用户自己在指定教学周的课程安排",
			Examples:         []string{"查我的课表", "我这周课程安排看一下"},
			NegativeExamples: []string{"查询张三的课表", "课表规则是什么"},
			RawSlots: []RawSlotSpec{
				{RawName: "week", TargetParam: "week", Resolver: "week_slot"},
			},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Resolvers:  []ResolverSpec{{Param: "week", Name: "week_slot"}},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchSchedule},
		Policies:   []PolicySpec{{Name: "conversation_scope"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingScheduleQueryMySchedule},
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
		Recognition: RecognitionSpec{
			Description: "查询指定用户在指定教学周的课程安排",
			Examples: []string{
				"查询一下杨思见的课程信息",
				"查张三本周课表",
			},
			NegativeExamples: []string{"查我的课表", "课表规则是什么"},
			RawSlots: []RawSlotSpec{
				{RawName: "user_name", TargetParam: "user_id", Resolver: "user_resolver", Required: true},
				{RawName: "user", TargetParam: "user_id", Resolver: "user_resolver"},
				{RawName: "week", TargetParam: "week", Resolver: "week_slot"},
			},
		},
		Workflow:   singleTurnWorkflowSpec(),
		Resolvers:  []ResolverSpec{{Param: "user_id", Name: "user_resolver"}, {Param: "week", Name: "week_slot"}},
		Dispatch:   ProtocolLiveDispatchBinding{Name: ProtocolLiveDispatchSchedule},
		Policies:   []PolicySpec{{Name: "conversation_scope"}, {Name: "schedule_user_visibility"}},
		WriteGuard: WriteGuardBinding{Name: WriteGuardBindingNotRequired},
		Executor:   ExecutorBinding{Name: ExecutorBindingScheduleQueryUserSchedule},
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
			Description:           manifest.Recognition.Description,
			RequiredTrustedParams: paramNames(manifest.RequiredTrustedParams),
			OptionalTrustedParams: paramNames(manifest.OptionalTrustedParams),
			Defaults:              cloneSlotDefaults(manifest.Defaults),
			Aliases:               append([]string(nil), manifest.Recognition.Aliases...),
			Examples:              append([]string(nil), manifest.Recognition.Examples...),
			NegativeExamples:      append([]string(nil), manifest.Recognition.NegativeExamples...),
			RawSlots:              append([]RawSlotSpec(nil), manifest.Recognition.RawSlots...),
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

func lintOperationCatalog(entries []OperationManifest) []string { //nolint:gocyclo // Catalog lint keeps manifest invariants in one reviewable pass.
	var errs []string
	seen := make(map[string]struct{}, len(entries))
	for i, manifest := range entries {
		prefix := fmt.Sprintf("operation[%d]", i)
		if strings.TrimSpace(manifest.Name) == "" {
			errs = append(errs, prefix+": name is required")
			continue
		}
		prefix = manifest.Name
		if strings.TrimSpace(manifest.Recognition.Description) == "" {
			errs = append(errs, prefix+": recognition description is required")
		}
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
		} else if _, ok := lookupOperationExecutorBinding(manifest.Executor.Name); !ok {
			errs = append(errs, prefix+": executor binding is unknown")
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
		errs = append(errs, lintRawSlotContract(manifest)...)
		for _, shape := range manifest.QueryShapes {
			errs = append(errs, lintParamResolution(manifest, shape.RequiredTrustedParams)...)
		}
	}
	return errs
}

func lintRawSlotContract(manifest OperationManifest) []string {
	var errs []string
	seen := make(map[string]struct{}, len(manifest.Recognition.RawSlots))
	for _, slot := range manifest.Recognition.RawSlots {
		if strings.TrimSpace(slot.RawName) == "" || strings.TrimSpace(slot.TargetParam) == "" || strings.TrimSpace(slot.Resolver) == "" {
			errs = append(errs, manifest.Name+": raw slot requires raw name, target param, and resolver")
			continue
		}
		if _, ok := seen[slot.RawName]; ok {
			errs = append(errs, fmt.Sprintf("%s: duplicate raw slot %q", manifest.Name, slot.RawName))
		}
		seen[slot.RawName] = struct{}{}
		errs = append(errs, lintRawSlotDefinition(manifest, slot)...)
	}
	return errs
}

func lintRawSlotDefinition(manifest OperationManifest, slot RawSlotSpec) []string {
	var errs []string
	if trustedIDSlotField(slot.RawName) {
		errs = append(errs, fmt.Sprintf("%s: raw slot %q must not be a trusted id", manifest.Name, slot.RawName))
	}
	if !rawSlotTargetDeclared(manifest, slot.TargetParam) {
		errs = append(errs, fmt.Sprintf("%s: raw slot %q targets undeclared param %q", manifest.Name, slot.RawName, slot.TargetParam))
	}
	if slot.Required && !paramSpecListContains(manifest.RequiredTrustedParams, slot.TargetParam) {
		errs = append(errs, fmt.Sprintf("%s: required raw slot %q must target a required trusted param", manifest.Name, slot.RawName))
	}
	if !rawSlotResolverDeclared(manifest, slot) {
		errs = append(errs, fmt.Sprintf("%s: raw slot %q resolver %q does not match target param %q", manifest.Name, slot.RawName, slot.Resolver, slot.TargetParam))
	}
	if !knownRawSlotShape(slot.Shape) {
		errs = append(errs, fmt.Sprintf("%s: raw slot %q has unknown workflow shape %q", manifest.Name, slot.RawName, slot.Shape))
	}
	return errs
}

func rawSlotTargetDeclared(manifest OperationManifest, target string) bool {
	return paramSpecListContains(manifest.RequiredTrustedParams, target) ||
		paramSpecListContains(manifest.OptionalTrustedParams, target) ||
		queryShapesRequireTrustedParam(manifest.QueryShapes, target)
}

func rawSlotResolverDeclared(manifest OperationManifest, slot RawSlotSpec) bool {
	for _, resolver := range manifest.Resolvers {
		if resolver.Param == slot.TargetParam && resolver.Name == slot.Resolver {
			return true
		}
	}
	return false
}

func knownRawSlotShape(shape string) bool {
	return shape == "" || shape == "subscription_scope" || shape == "department_name_or_candidate"
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
	if manifest.Idempotency.Guarantee == "" {
		errs = append(errs, prefix+": write idempotency guarantee is required")
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
