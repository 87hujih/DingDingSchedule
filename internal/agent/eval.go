package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

// EvalCase 表示一条离线评测样本。
type EvalCase struct {
	Name                      string   `json:"name"`
	Category                  string   `json:"category"`
	Question                  string   `json:"question"`
	ExpectedDomain            string   `json:"expected_domain,omitempty"`
	ExpectedPlanKind          string   `json:"expected_plan_kind,omitempty"`
	ExpectedIntent            string   `json:"expected_intent,omitempty"`
	ExpectedExecutor          string   `json:"expected_executor,omitempty"`
	ExpectedMode              string   `json:"expected_mode,omitempty"`
	ExpectedRoute             string   `json:"expected_route,omitempty"`
	ExpectedProtocolAct       string   `json:"expected_protocol_act,omitempty"`
	ExpectedProtocolDomain    string   `json:"expected_protocol_domain,omitempty"`
	ExpectedProtocolOperation string   `json:"expected_protocol_operation,omitempty"`
	ExpectedResponseKind      string   `json:"expected_response_kind,omitempty"`
	ExpectedBlockedReason     string   `json:"expected_blocked_reason,omitempty"`
	ExpectedFailureLayer      string   `json:"expected_failure_layer,omitempty"`
	ExpectedLegacyCalled      *bool    `json:"expected_legacy_called,omitempty"`
	ExpectedWorkflowDecision  string   `json:"expected_workflow_decision,omitempty"`
	ExpectedWorkflowReason    string   `json:"expected_workflow_interrupt_reason,omitempty"`
	ConversationType          string   `json:"conversation_type,omitempty"`
	ActiveWorkflowType        string   `json:"active_workflow_type,omitempty"`
	ActiveWorkflowState       string   `json:"active_workflow_state,omitempty"`
	ActiveWorkflowScope       string   `json:"active_workflow_scope,omitempty"`
	ActiveWorkflowMissing     []string `json:"active_workflow_missing,omitempty"`
	ActiveWorkflowExpired     bool     `json:"active_workflow_expired,omitempty"`
	ExpectedTools             []string `json:"expected_tools,omitempty"`
	ExpectedSources           []string `json:"expected_sources,omitempty"`
	ExpectedKeywords          []string `json:"expected_keywords,omitempty"`

	ExpectedFailureLayerSet bool `json:"-"`
}

// EvalObservation 表示一次端到端问答观测结果。
type EvalObservation struct {
	Reply                 string
	Tools                 []string
	ProtocolAct           string
	ProtocolDomain        string
	ProtocolOperation     string
	ResponseKind          string
	ProtocolBlockedReason string
	FailureLayer          string
	LegacyCalled          bool
	WorkflowDecision      string
	WorkflowReason        string
}

// EvalObserver 执行真实问答并返回回复与工具调用信息。
type EvalObserver func(ctx context.Context, tc EvalCase) (EvalObservation, error)

// EvalCaseResult 表示一条样本的评测结果。
type EvalCaseResult struct {
	Name                  string
	Category              string
	Question              string
	DomainHint            string
	DomainResult          string
	DomainMatched         bool
	PlanKind              string
	PlanChecked           bool
	PlanMatched           bool
	KnowledgeStrength     string
	PlannerReason         string
	Intent                string
	IntentChecked         bool
	IntentMatched         bool
	Executor              string
	ExecutorChecked       bool
	ExecutorMatched       bool
	ProtocolAct           string
	ProtocolDomain        string
	ProtocolOperation     string
	ResponseKind          string
	ProtocolBlockedReason string
	FailureLayer          string
	LegacyCalled          bool
	WorkflowDecision      string
	WorkflowReason        string
	ProtocolChecked       bool
	ProtocolMatched       bool
	AnswerMode            string
	ModeMatched           bool
	Route                 string
	RouteMatched          bool
	RetrievalChecked      bool
	RetrievalMatched      bool
	RetrievedSources      []string
	ToolsChecked          bool
	ToolsMatched          bool
	NoWriteToolsChecked   bool
	NoWriteToolsMatched   bool
	ActualTools           []string
	KeywordsChecked       bool
	KeywordsMatched       bool
	Reply                 string
	DurationMs            int64
	Error                 string
}

func (tc *EvalCase) UnmarshalJSON(data []byte) error {
	type evalCaseNoMethods EvalCase
	var decoded evalCaseNoMethods
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*tc = EvalCase(decoded)
	_, tc.ExpectedFailureLayerSet = raw["expected_failure_layer"]
	return nil
}

// EvalSummary 表示整批样本的评测摘要。
type EvalSummary struct {
	TotalCases        int
	DomainPassed      int
	DomainAccuracy    float64
	PlanCases         int
	PlanPassed        int
	PlanAccuracy      float64
	IntentCases       int
	IntentPassed      int
	IntentAccuracy    float64
	ExecutorCases     int
	ExecutorPassed    int
	ExecutorAccuracy  float64
	ProtocolCases     int
	ProtocolPassed    int
	ProtocolAccuracy  float64
	ModePassed        int
	ModeAccuracy      float64
	RoutePassed       int
	RouteAccuracy     float64
	RetrievalCases    int
	RetrievalPassed   int
	RetrievalAccuracy float64
	ToolCases         int
	ToolPassed        int
	ToolAccuracy      float64
	KeywordCases      int
	KeywordPassed     int
	KeywordAccuracy   float64
	AverageLatencyMs  int64
}

// LoadEvalCases 从 JSON 文件加载评测样本。
func LoadEvalCases(path string) ([]EvalCase, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read eval cases: %w", err)
	}

	var cases []EvalCase
	if err := json.Unmarshal(content, &cases); err != nil {
		return nil, fmt.Errorf("decode eval cases: %w", err)
	}
	return cases, nil
}

// EvaluateCases 评估 unified planner、知识检索以及可选的端到端问答结果。
func EvaluateCases(ctx context.Context, knowledge KnowledgePort, tenantID uint, cases []EvalCase, observer EvalObserver) (EvalSummary, []EvalCaseResult, error) {
	results := make([]EvalCaseResult, 0, len(cases))
	summary := EvalSummary{TotalCases: len(cases)}

	var totalLatency int64

	for _, tc := range cases {
		start := time.Now()
		result := EvalCaseResult{
			Name:     tc.Name,
			Category: tc.Category,
			Question: tc.Question,
		}

		normalized := normalizeQuery(tc.Question)
		conversationDecision := interpretConversation(tc.Question, nil)

		result.DomainResult = string(evalDomainResult(normalized))
		expectedDomain := strings.TrimSpace(tc.ExpectedDomain)
		if expectedDomain == "" {
			expectedDomain = result.DomainResult
		}
		result.DomainMatched = strings.EqualFold(result.DomainResult, expectedDomain)
		if result.DomainMatched {
			summary.DomainPassed++
		}

		userCtx := evalUserContext(tenantID)
		taskCandidate := buildTaskFromRequest(tc.Question, userCtx)

		retrievalResult := RetrievalResult{}
		var retrievalErr error
		if taskCandidate == nil {
			retrievalResult, retrievalErr = searchEvalKnowledge(ctx, knowledge, tenantID, tc.Question)
			if retrievalErr != nil {
				result.Error = retrievalErr.Error()
			}
		}

		planDecision := evalPlanDecision(normalized, conversationDecision, retrievalResult, taskCandidate, userCtx, tc.Question)
		result.PlanKind = string(planDecision.Kind)
		result.KnowledgeStrength = string(planDecision.KnowledgeStrength)
		result.PlannerReason = planDecision.ClarifyReason

		expectedPlanKind := defaultExpectedPlanKind(tc)
		if expectedPlanKind != "" {
			result.PlanChecked = true
			summary.PlanCases++
			result.PlanMatched = strings.EqualFold(result.PlanKind, expectedPlanKind)
			if result.PlanMatched {
				summary.PlanPassed++
			}
		}

		compat := evalCompatForPlan(normalized, taskCandidate, planDecision)
		result.Intent = compat.Intent
		expectedIntent := defaultExpectedIntent(tc)
		if expectedIntent != "" {
			result.IntentChecked = true
			summary.IntentCases++
			result.IntentMatched = strings.EqualFold(result.Intent, expectedIntent)
			if result.IntentMatched {
				summary.IntentPassed++
			}
		}

		result.Executor = compat.Executor
		expectedExecutor := defaultExpectedExecutor(tc)
		if expectedExecutor != "" {
			result.ExecutorChecked = true
			summary.ExecutorCases++
			result.ExecutorMatched = strings.EqualFold(result.Executor, expectedExecutor)
			if result.ExecutorMatched {
				summary.ExecutorPassed++
			}
		}

		if protocolExpectationPresent(tc) {
			protocolEval := evaluateProtocolCase(ctx, tc, knowledge, tenantID)
			result.ProtocolAct = protocolEval.Act
			result.ProtocolDomain = protocolEval.Domain
			result.ProtocolOperation = protocolEval.Operation
			result.ResponseKind = protocolEval.ResponseKind
			result.ProtocolBlockedReason = protocolEval.BlockedReason
			result.FailureLayer = protocolEval.FailureLayer
			result.LegacyCalled = protocolEval.LegacyCalled
			result.WorkflowDecision = protocolEval.WorkflowDecision
			result.WorkflowReason = protocolEval.WorkflowReason
		}

		result.AnswerMode = string(compat.AnswerMode)
		expectedMode := strings.TrimSpace(tc.ExpectedMode)
		if expectedMode == "" {
			expectedMode = defaultExpectedMode(tc)
		}
		result.ModeMatched = expectedMode == "" || strings.EqualFold(result.AnswerMode, expectedMode)
		if result.ModeMatched {
			summary.ModePassed++
		}

		result.Route = string(compat.Route)
		expectedRoute := strings.TrimSpace(tc.ExpectedRoute)
		if expectedRoute == "" {
			if protocolExpectationPresent(tc) {
				// Protocol-only samples validate protocol act/domain/operation. Legacy route is checked only when explicit.
				expectedRoute = result.Route
			} else if expectedRoute = string(modeToQueryKind(answerModeForExpectedMode(expectedMode))); expectedRoute == "" {
				expectedRoute = strings.TrimSpace(tc.Category)
			}
		}
		result.RouteMatched = expectedRoute == "" || strings.EqualFold(result.Route, expectedRoute)
		if result.RouteMatched {
			summary.RoutePassed++
		}

		if len(tc.ExpectedSources) > 0 {
			result.RetrievalChecked = true
			summary.RetrievalCases++
			if result.Error == "" {
				result.RetrievedSources = collectSourceRefs(retrievalResult.Hits)
				result.RetrievalMatched = matchAnyNormalized(result.RetrievedSources, tc.ExpectedSources)
				if result.RetrievalMatched {
					summary.RetrievalPassed++
				}
			}
		}

		if observer != nil {
			observation, err := observer(ctx, tc)
			if err != nil {
				if result.Error == "" {
					result.Error = err.Error()
				}
			} else {
				result.Reply = observation.Reply
				result.ActualTools = observation.Tools
				if observation.ProtocolAct != "" {
					result.ProtocolAct = observation.ProtocolAct
				}
				if observation.ProtocolDomain != "" {
					result.ProtocolDomain = observation.ProtocolDomain
				}
				if observation.ProtocolOperation != "" {
					result.ProtocolOperation = observation.ProtocolOperation
				}
				if observation.ResponseKind != "" {
					result.ResponseKind = observation.ResponseKind
				}
				if observation.ProtocolBlockedReason != "" {
					result.ProtocolBlockedReason = observation.ProtocolBlockedReason
				}
				if observation.FailureLayer != "" {
					result.FailureLayer = observation.FailureLayer
				}
				result.LegacyCalled = observation.LegacyCalled
				if observation.WorkflowDecision != "" {
					result.WorkflowDecision = observation.WorkflowDecision
				}
				if observation.WorkflowReason != "" {
					result.WorkflowReason = observation.WorkflowReason
				}
			}
		}

		if protocolExpectationPresent(tc) {
			result.ProtocolChecked = true
			summary.ProtocolCases++
			result.ProtocolMatched = protocolExpectationMatched(tc, result)
			if result.ProtocolMatched {
				summary.ProtocolPassed++
			}
		}

		if tc.ExpectedTools != nil {
			if len(tc.ExpectedTools) == 0 {
				result.ToolsChecked = true
				summary.ToolCases++
				result.ToolsMatched = len(result.ActualTools) == 0
				result.NoWriteToolsChecked = true
				result.NoWriteToolsMatched = noWriteToolCalls(result.ActualTools)
			} else if observer != nil {
				result.ToolsChecked = true
				summary.ToolCases++
				result.ToolsMatched = containsAllNormalized(result.ActualTools, tc.ExpectedTools)
			}
			if result.ToolsChecked && result.ToolsMatched {
				summary.ToolPassed++
			}
		}

		if observer != nil {
			if len(tc.ExpectedKeywords) > 0 {
				result.KeywordsChecked = true
				summary.KeywordCases++
				result.KeywordsMatched = containsAllKeywords(result.Reply, tc.ExpectedKeywords)
				if result.KeywordsMatched {
					summary.KeywordPassed++
				}
			}
		}

		result.DurationMs = time.Since(start).Milliseconds()
		totalLatency += result.DurationMs
		results = append(results, result)
	}

	summary.DomainAccuracy = percent(summary.DomainPassed, summary.TotalCases)
	summary.PlanAccuracy = percent(summary.PlanPassed, summary.PlanCases)
	summary.IntentAccuracy = percent(summary.IntentPassed, summary.IntentCases)
	summary.ExecutorAccuracy = percent(summary.ExecutorPassed, summary.ExecutorCases)
	summary.ProtocolAccuracy = percent(summary.ProtocolPassed, summary.ProtocolCases)
	summary.ModeAccuracy = percent(summary.ModePassed, summary.TotalCases)
	summary.RouteAccuracy = percent(summary.RoutePassed, summary.TotalCases)
	summary.RetrievalAccuracy = percent(summary.RetrievalPassed, summary.RetrievalCases)
	summary.ToolAccuracy = percent(summary.ToolPassed, summary.ToolCases)
	summary.KeywordAccuracy = percent(summary.KeywordPassed, summary.KeywordCases)
	if summary.TotalCases > 0 {
		summary.AverageLatencyMs = totalLatency / int64(summary.TotalCases)
	}

	return summary, results, nil
}

// evalUserContext handles eval user context.
func evalUserContext(tenantID uint) *tools.UserContext {
	if tenantID == 0 {
		tenantID = 1
	}
	return &tools.UserContext{
		TenantID:          tenantID,
		UserID:            1,
		UserRole:          1,
		DingUserID:        "eval-user",
		Name:              "EvalUser",
		ConversationType:  "2",
		ConversationID:    "eval-conversation",
		ConversationTitle: "评测群",
	}
}

type evalProtocolResult struct {
	Act              string
	Domain           string
	Operation        string
	ResponseKind     string
	BlockedReason    string
	FailureLayer     string
	LegacyCalled     bool
	WorkflowDecision string
	WorkflowReason   string
}

type evalProtocolCompiler struct{}

func (evalProtocolCompiler) Compile(_ context.Context, req IntentCompileRequest) (IntentDraft, error) {
	normalized := normalizeQuery(req.Message)
	if draft, ok := compileCapabilityQuestion(req.Message); ok {
		return draft, nil
	}
	if hasHelpIntent(normalized) {
		operation, ok := operationNameForActDomain(ActHelp, DomainSystem)
		if !ok {
			return unknownIntentDraft("operation_not_allowed"), nil
		}
		return IntentDraft{
			Act:        ActHelp,
			Domain:     DomainSystem,
			Operation:  operation,
			Confidence: 1,
		}, nil
	}
	var workflow *protocolWorkflowContext
	if req.ActiveWorkflow != nil {
		workflow = &protocolWorkflowContext{
			Type:          req.ActiveWorkflow.Type,
			MissingFields: append([]string(nil), req.ActiveWorkflow.MissingFields...),
		}
	}
	return compileProtocol(protocolInput{
		Message:        req.Message,
		ActiveWorkflow: workflow,
	}), nil
}

func evaluateProtocolCase(ctx context.Context, tc EvalCase, knowledge KnowledgePort, tenantID uint) evalProtocolResult {
	uctx := evalUserContext(tenantID)
	if conversationType := strings.TrimSpace(tc.ConversationType); conversationType != "" {
		uctx.ConversationType = conversationType
	}
	var activeWorkflow *WorkflowSnapshot
	if strings.TrimSpace(tc.ActiveWorkflowType) != "" {
		activeWorkflow = &WorkflowSnapshot{
			ID:             "eval-workflow",
			TenantID:       uctx.TenantID,
			ActorUserID:    uctx.UserID,
			ConversationID: uctx.ConversationID,
			Type:           WorkflowType(strings.TrimSpace(tc.ActiveWorkflowType)),
			State:          WorkflowState(strings.TrimSpace(tc.ActiveWorkflowState)),
			MissingFields:  append([]string(nil), tc.ActiveWorkflowMissing...),
			MissingSlots:   append([]string(nil), tc.ActiveWorkflowMissing...),
		}
		if scope := strings.TrimSpace(tc.ActiveWorkflowScope); scope != "" {
			activeWorkflow.Trusted.TenantID = uctx.TenantID
			activeWorkflow.Trusted.Scope = scope
			activeWorkflow.Trusted.TrustedParams = map[string]TrustedParam{
				"scope": trustedParam("scope", scope, uctx.TenantID, TrustedParamSource{
					Kind:     TrustedParamSourceWorkflow,
					Resolver: "eval_fixture",
				}),
			}
		}
		if tc.ActiveWorkflowExpired {
			activeWorkflow.ExpiresAt = time.Now().Add(-time.Minute)
		}
	}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler:       evalProtocolCompiler{},
		User:           evalUserPort{tenantID: tenantID},
		Dept:           evalDeptPort{tenantID: tenantID},
		Semester:       evalSemesterPort{},
		SchedulePeriod: evalSchedulePeriodPort{},
		Executor: newOperationExecutor(operationExecutorDeps{
			Schedule:   evalSchedulePort{},
			Attendance: evalAttendancePort{},
			Semester:   evalSemesterPort{},
			Dept:       evalDeptPort{tenantID: tenantID},
			GroupSub:   evalGroupSubPort{},
			Knowledge:  knowledge,
		}),
	})
	outcome := pipeline.Handle(ctx, protocolLiveInput{
		Message:        tc.Question,
		User:           uctx,
		ActiveWorkflow: activeWorkflow,
	})
	return evalProtocolResult{
		Act:              string(outcome.Draft.Act),
		Domain:           string(outcome.Draft.Domain),
		Operation:        outcome.Draft.Operation,
		ResponseKind:     string(outcome.Response.Kind),
		BlockedReason:    outcome.BlockedReason,
		FailureLayer:     string(outcome.FailureLayer),
		LegacyCalled:     outcome.LegacyCalled,
		WorkflowDecision: string(outcome.WorkflowDecision),
		WorkflowReason:   outcome.WorkflowInterruptReason,
	}
}

type evalDeptPort struct {
	tenantID uint
}

func (p evalDeptPort) ListDepts(context.Context) ([]tools.DeptItem, error) {
	tenantID := p.tenantID
	if tenantID == 0 {
		tenantID = 1
	}
	return []tools.DeptItem{
		{TenantID: tenantID, DeptID: 101, Name: "信工24级"},
		{TenantID: tenantID, DeptID: 102, Name: "信工25级"},
	}, nil
}

type evalUserPort struct {
	tenantID uint
}

func (p evalUserPort) FindByDingUserID(context.Context, string) (*tools.UserInfo, error) {
	tenantID := p.tenantID
	if tenantID == 0 {
		tenantID = 1
	}
	return &tools.UserInfo{ID: 1, Name: "EvalUser", DingUserID: "eval-user", Role: 1, TenantID: tenantID}, nil
}

func (p evalUserPort) SearchByName(_ context.Context, name string) ([]tools.UserInfo, error) {
	tenantID := p.tenantID
	if tenantID == 0 {
		tenantID = 1
	}
	if strings.TrimSpace(name) == "张三" {
		return []tools.UserInfo{{ID: 7, Name: "张三", DingUserID: "ding-zhangsan", Role: 0, TenantID: tenantID}}, nil
	}
	return nil, nil
}

type evalGroupSubPort struct{}

func (evalGroupSubPort) Subscribe(context.Context, uint, string, string, uint, []int64) error {
	return nil
}

func (evalGroupSubPort) Unsubscribe(context.Context, uint, string) error {
	return nil
}

func (evalGroupSubPort) GetSubscription(context.Context, uint, string) (*tools.GroupSubInfo, error) {
	return &tools.GroupSubInfo{Subscribed: false}, nil
}

type evalSemesterPort struct{}

func (evalSemesterPort) GetCurrentWeek(context.Context) (int, int, error) {
	return 3, 20, nil
}

type evalSchedulePeriodPort struct{}

func (evalSchedulePeriodPort) GetScheduleInfo(context.Context) ([]tools.PeriodInfo, string, error) {
	return []tools.PeriodInfo{
		{Name: "第一节", Start: "08:00", End: "08:45"},
		{Name: "第二节", Start: "08:55", End: "09:40"},
	}, "standard", nil
}

type evalSchedulePort struct{}

func (evalSchedulePort) ListMyScheduleByWeek(context.Context, uint, int) ([]tools.CourseItem, error) {
	return []tools.CourseItem{{CourseName: "高等数学", DayOfWeek: 1, Section: 1, Location: "A101", Teacher: "王老师", WeekList: "1-16"}}, nil
}

func (evalSchedulePort) ListUserScheduleByWeek(context.Context, uint, int, uint, int) ([]tools.CourseItem, error) {
	return []tools.CourseItem{{CourseName: "数据结构", DayOfWeek: 2, Section: 2, Location: "B202", Teacher: "李老师", WeekList: "1-16"}}, nil
}

func (evalSchedulePort) GetFreeUsersBySlot(context.Context, int, int, int, int64) ([]tools.FreeSlotResult, error) {
	return nil, nil
}

type evalAttendancePort struct{}

func (evalAttendancePort) GetAttendanceDetail(_ context.Context, req tools.AttendanceQuery) (*tools.AttendanceResult, error) {
	return &tools.AttendanceResult{
		Date:         req.Date,
		Week:         req.Week,
		Section:      req.Section,
		ViewMode:     "realtime",
		ShouldAttend: 2,
		OnTimeCount:  1,
		AbsentCount:  1,
		OnTimeUsers:  []string{"张三"},
		AbsentUsers:  []string{"李四"},
	}, nil
}

func (evalAttendancePort) GetAttendanceText(context.Context, tools.AttendanceQuery) (string, error) {
	return "考勤通报：应到2人，未到1人。", nil
}

func (evalAttendancePort) GetWeeklyAbsenceRanking(context.Context) ([]tools.RankItem, error) {
	return nil, nil
}

func (evalAttendancePort) GetWeeklyAttendanceRateRanking(context.Context) ([]tools.RankItem, error) {
	return nil, nil
}

func (evalAttendancePort) FindRecordByDateSection(context.Context, string, int) (uint, error) {
	return 1, nil
}

func (evalAttendancePort) SignForUsers(context.Context, uint, []uint) error {
	return nil
}

func (evalAttendancePort) SignForUsersBySlot(context.Context, string, int, []uint) error {
	return nil
}

func protocolExpectationPresent(tc EvalCase) bool {
	return strings.TrimSpace(tc.ExpectedProtocolAct) != "" ||
		strings.TrimSpace(tc.ExpectedProtocolDomain) != "" ||
		strings.TrimSpace(tc.ExpectedProtocolOperation) != "" ||
		strings.TrimSpace(tc.ExpectedResponseKind) != "" ||
		strings.TrimSpace(tc.ExpectedBlockedReason) != "" ||
		expectedFailureLayerPresent(tc) ||
		tc.ExpectedLegacyCalled != nil ||
		strings.TrimSpace(tc.ExpectedWorkflowDecision) != "" ||
		strings.TrimSpace(tc.ExpectedWorkflowReason) != ""
}

func protocolExpectationMatched(tc EvalCase, result EvalCaseResult) bool {
	checks := []struct {
		expected string
		actual   string
	}{
		{tc.ExpectedProtocolAct, result.ProtocolAct},
		{tc.ExpectedProtocolDomain, result.ProtocolDomain},
		{tc.ExpectedProtocolOperation, result.ProtocolOperation},
		{tc.ExpectedResponseKind, result.ResponseKind},
		{tc.ExpectedBlockedReason, result.ProtocolBlockedReason},
		{tc.ExpectedWorkflowDecision, result.WorkflowDecision},
		{tc.ExpectedWorkflowReason, result.WorkflowReason},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.expected) == "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(check.actual), strings.TrimSpace(check.expected)) {
			return false
		}
	}
	if expectedFailureLayerPresent(tc) && !strings.EqualFold(strings.TrimSpace(result.FailureLayer), strings.TrimSpace(tc.ExpectedFailureLayer)) {
		return false
	}
	if result.LegacyCalled {
		return false
	}
	if tc.ExpectedLegacyCalled != nil && result.LegacyCalled != *tc.ExpectedLegacyCalled {
		return false
	}
	return true
}

func expectedFailureLayerPresent(tc EvalCase) bool {
	return tc.ExpectedFailureLayerSet || strings.TrimSpace(tc.ExpectedFailureLayer) != ""
}

func noWriteToolCalls(toolsCalled []string) bool {
	for _, toolName := range toolsCalled {
		switch strings.TrimSpace(toolName) {
		case "sign_for_user", "subscribe_attendance_push", "unsubscribe_attendance_push":
			return false
		}
	}
	return true
}

// evalPlanDecision handles eval plan decision.
func evalPlanDecision(
	normalized string,
	conversationDecision conversationDecision,
	retrievalResult RetrievalResult,
	taskCandidate *ActiveTask,
	userCtx *tools.UserContext,
	question string,
) PlanDecision {
	if conversationDecision.Event == eventGreeting {
		return PlanDecision{
			Kind:              planKindTool,
			ClarifyReason:     conversationDecision.Reason,
			KnowledgeStrength: knowledgeStrengthNone,
		}
	}
	if hasHelpIntent(normalized) {
		return PlanDecision{
			Kind:              planKindTool,
			ClarifyReason:     "help_intent",
			KnowledgeStrength: knowledgeStrengthNone,
		}
	}
	if evalDomainResult(normalized) == domainOut {
		return PlanDecision{
			Kind:              planKindObviousOut,
			ClarifyReason:     "out_of_domain",
			KnowledgeStrength: knowledgeStrengthNone,
		}
	}
	return plan(PlanInput{
		Question:          question,
		UserContext:       userCtx,
		History:           nil,
		ActiveTask:        nil,
		ConversationEvent: conversationDecision,
		Retrieval:         retrievalResult,
		TaskCandidate:     taskCandidate,
		HasLiveSignal:     hasLiveSignal(normalized),
		HasRuleSignal:     hasRuleSignal(normalized),
		HasActionIntent:   hasActionIntent(normalized),
		HasClarifyIntent:  hasClarifyIntent(normalized),
		HasHelpIntent:     hasHelpIntent(normalized),
	})
}

// evalDomainResult mirrors the semantic-router reject target for deterministic offline eval cases.
func evalDomainResult(normalized string) domainResult {
	if strings.TrimSpace(normalized) == "" {
		return domainIn
	}
	if containsAny(normalized, []string{
		"考勤",
		"课表",
		"课程",
		"排课",
		"请假",
		"休息日",
		"订阅",
		"推送",
		"补签",
		"代签",
		"部门",
		"作息",
		"节次",
		"迟到",
		"未到",
		"缺勤",
		"出勤",
		"钉钉",
	}) {
		return domainIn
	}
	if containsAny(normalized, []string{
		"天气",
		"气温",
		"下雨",
		"空气质量",
		"股票",
		"新闻",
		"二分查找",
		"排序算法",
		"写代码",
		"写一个",
		"编程",
		"算法",
	}) {
		return domainOut
	}
	return domainIn
}

type evalCompatDecision struct {
	Intent     string
	Executor   string
	AnswerMode answerMode
	Route      queryKind
}

// evalCompatForPlan returns eval compat for plan.
func evalCompatForPlan(normalized string, taskCandidate *ActiveTask, decision PlanDecision) evalCompatDecision {
	if hasHelpIntent(normalized) {
		return evalCompatDecision{
			Intent:     string(intentHelp),
			Executor:   "help",
			AnswerMode: answerModeToolFirst,
			Route:      queryKindTool,
		}
	}

	switch decision.Kind {
	case planKindObviousOut:
		return evalCompatDecision{
			Executor:   "reject",
			AnswerMode: answerModeReject,
			Route:      queryKindTool,
		}
	case planKindClarify:
		return evalCompatDecision{
			Intent:     string(intentClarify),
			Executor:   "clarify",
			AnswerMode: answerModeToolFirst,
			Route:      queryKindTool,
		}
	case planKindRAG:
		return evalCompatDecision{
			Intent:     string(intentRule),
			Executor:   "knowledge",
			AnswerMode: answerModeKnowledgeOnly,
			Route:      queryKindRAG,
		}
	case planKindMixed:
		return evalCompatDecision{
			Intent:     string(intentMixed),
			Executor:   "mixed",
			AnswerMode: answerModeMixed,
			Route:      queryKindMixed,
		}
	case planKindContinueTask:
		return evalCompatDecision{
			Intent:     string(intentAction),
			Executor:   "tool",
			AnswerMode: answerModeToolFirst,
			Route:      queryKindTool,
		}
	case planKindTool:
		intent := string(intentAction)
		if taskCandidate == nil {
			switch {
			case hasLiveSignal(normalized):
				intent = string(intentLiveQuery)
			case hasActionIntent(normalized):
				intent = string(intentAction)
			case hasClarifyIntent(normalized):
				intent = string(intentClarify)
			case hasRuleSignal(normalized):
				intent = string(intentRule)
			}
		}
		return evalCompatDecision{
			Intent:     intent,
			Executor:   "tool",
			AnswerMode: answerModeToolFirst,
			Route:      queryKindTool,
		}
	default:
		return evalCompatDecision{}
	}
}

// searchEvalKnowledge handles search eval knowledge.
func searchEvalKnowledge(ctx context.Context, knowledge KnowledgePort, tenantID uint, question string) (RetrievalResult, error) {
	if knowledge == nil || tenantID == 0 {
		return RetrievalResult{}, nil
	}

	hits, err := knowledge.Search(ctx, tenantID, question, defaultKnowledgeTopK)
	if err != nil {
		return RetrievalResult{}, err
	}

	result := RetrievalResult{
		Hits:              hits,
		CandidateCount:    len(hits),
		TopRefs:           collectSourceRefs(hits),
		TopScores:         collectKnowledgeScores(hits),
		KnowledgeDocTypes: collectKnowledgeDocTypes(hits),
	}
	if len(hits) == 0 {
		result.FilteredReason = retrievalFilteredReasonNoHits
	}
	return result, nil
}

// defaultExpectedPlanKind handles default expected plan kind.
func defaultExpectedPlanKind(tc EvalCase) string {
	if planKind := strings.TrimSpace(tc.ExpectedPlanKind); planKind != "" {
		return planKind
	}

	if executor := strings.TrimSpace(tc.ExpectedExecutor); executor != "" {
		switch executor {
		case "help":
			return string(planKindTool)
		case "clarify":
			return string(planKindClarify)
		case "knowledge":
			return string(planKindRAG)
		case "mixed":
			return string(planKindMixed)
		case "reject":
			return string(planKindObviousOut)
		case "tool":
			return string(planKindTool)
		}
	}

	switch strings.TrimSpace(tc.ExpectedMode) {
	case string(answerModeKnowledgeOnly):
		return string(planKindRAG)
	case string(answerModeMixed):
		return string(planKindMixed)
	case string(answerModeReject):
		return string(planKindObviousOut)
	case string(answerModeToolFirst):
		return string(planKindTool)
	}

	switch strings.TrimSpace(tc.Category) {
	case "rag":
		return string(planKindRAG)
	case "mixed":
		return string(planKindMixed)
	case "reject":
		return string(planKindObviousOut)
	case "tool":
		return string(planKindTool)
	default:
		return ""
	}
}

// defaultExpectedIntent handles default expected intent.
func defaultExpectedIntent(tc EvalCase) string {
	if intent := strings.TrimSpace(tc.ExpectedIntent); intent != "" {
		return intent
	}

	switch defaultExpectedPlanKind(tc) {
	case string(planKindClarify):
		return string(intentClarify)
	case string(planKindRAG):
		return string(intentRule)
	case string(planKindMixed):
		return string(intentMixed)
	default:
		return ""
	}
}

// defaultExpectedExecutor handles default expected executor.
func defaultExpectedExecutor(tc EvalCase) string {
	if executor := strings.TrimSpace(tc.ExpectedExecutor); executor != "" {
		return executor
	}

	switch defaultExpectedPlanKind(tc) {
	case string(planKindClarify):
		return "clarify"
	case string(planKindRAG):
		return "knowledge"
	case string(planKindMixed):
		return "mixed"
	case string(planKindTool), string(planKindContinueTask):
		return "tool"
	case string(planKindObviousOut):
		return "reject"
	default:
		return ""
	}
}

// defaultExpectedMode handles default expected mode.
func defaultExpectedMode(tc EvalCase) string {
	if mode := strings.TrimSpace(tc.ExpectedMode); mode != "" {
		return mode
	}

	switch defaultExpectedPlanKind(tc) {
	case string(planKindRAG):
		return string(answerModeKnowledgeOnly)
	case string(planKindMixed):
		return string(answerModeMixed)
	case string(planKindObviousOut):
		return string(answerModeReject)
	case string(planKindTool), string(planKindContinueTask), string(planKindClarify):
		return string(answerModeToolFirst)
	default:
		return ""
	}
}

// answerModeForExpectedMode returns answer mode for expected mode.
func answerModeForExpectedMode(mode string) answerMode {
	switch strings.TrimSpace(mode) {
	case string(answerModeKnowledgeOnly):
		return answerModeKnowledgeOnly
	case string(answerModeMixed):
		return answerModeMixed
	case string(answerModeReject):
		return answerModeReject
	case string(answerModeToolFirst):
		return answerModeToolFirst
	default:
		return ""
	}
}

// collectSourceRefs 提取评测结果中的来源引用列表。
func collectSourceRefs(hits []KnowledgeHit) []string {
	sources := make([]string, 0, len(hits))
	for _, hit := range hits {
		source := strings.TrimSpace(hit.SourceRef)
		if source == "" {
			source = strings.TrimSpace(hit.Title)
		}
		if source == "" {
			continue
		}
		sources = append(sources, source)
	}
	return sources
}

// matchAnyNormalized 判断实际结果是否至少命中一个期望值。
func matchAnyNormalized(actual []string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, item := range actual {
		actualSet[normalizeEvalText(item)] = struct{}{}
	}
	for _, item := range expected {
		if _, ok := actualSet[normalizeEvalText(item)]; ok {
			return true
		}
	}
	return false
}

// containsAllNormalized 判断实际结果是否覆盖全部期望值。
func containsAllNormalized(actual []string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, item := range actual {
		actualSet[normalizeEvalText(item)] = struct{}{}
	}
	for _, item := range expected {
		if _, ok := actualSet[normalizeEvalText(item)]; !ok {
			return false
		}
	}
	return true
}

// containsAllKeywords 判断回复中是否包含全部期望关键词。
func containsAllKeywords(reply string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	normalizedReply := normalizeEvalText(reply)
	for _, keyword := range keywords {
		if !strings.Contains(normalizedReply, normalizeEvalText(keyword)) {
			return false
		}
	}
	return true
}

// normalizeEvalText 统一清理文本中的空白和常见标点。
func normalizeEvalText(text string) string {
	replacer := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\n", "",
		"\r", "",
		"，", "",
		"。", "",
		"？", "",
		"！", "",
		",", "",
		".", "",
		"?", "",
		"!", "",
	)
	return strings.ToLower(replacer.Replace(strings.TrimSpace(text)))
}

// percent 计算通过数在总数中的百分比。
func percent(passed int, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(passed) * 100 / float64(total)
}
