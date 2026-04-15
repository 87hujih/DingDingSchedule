package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"schedule_server/internal/agent/tools"
)

type routeExecutionResult struct {
	Reply        string
	ToolDefs     []tools.ToolDef
	Retrieval    RetrievalResult
	LLMDuration  int64
	ExecutorName string
	ToolPool     string
	AnswerMode   answerMode
}

type taskExecutionResult struct {
	Reply        string
	Task         *TaskInstance
	KeepTaskOpen bool
	ToolsCalled  []string
	MatchedSlots []string
	ExecutorName string
	AnswerMode   answerMode
}

type rejectExecutor struct{}

// Execute runs the current logic and returns the normalized result.
func (rejectExecutor) Execute() routeExecutionResult {
	return routeExecutionResult{
		Reply:        outOfDomainReply,
		ExecutorName: "reject_executor",
		AnswerMode:   answerModeReject,
	}
}

type socialExecutor struct{}

// Execute runs the current logic and returns the normalized result.
func (socialExecutor) Execute() routeExecutionResult {
	return routeExecutionResult{
		Reply:        composeRouteReply(RouteDecision{Kind: RouteSocialRefuse}, nil),
		ExecutorName: "social_executor",
		AnswerMode:   answerModeReject,
	}
}

type clarifyExecutor struct{}

// Execute runs the current logic and returns the normalized result.
func (clarifyExecutor) Execute(decision RouteDecision, task *TaskRouteState) routeExecutionResult {
	return routeExecutionResult{
		Reply:        composeRouteReply(decision, task),
		ExecutorName: "clarify_executor",
		AnswerMode:   answerModeToolFirst,
	}
}

type ragExecutor struct {
	agent *Agent
}

// Execute runs the current logic and returns the normalized result.
func (e ragExecutor) Execute(ctx context.Context, uctx *tools.UserContext, history []tools.Message, question string) (routeExecutionResult, error) {
	retrievalResult, err := e.agent.retrieveKnowledge(ctx, uctx.TenantID, question)
	if err != nil {
		return routeExecutionResult{}, err
	}

	result := routeExecutionResult{
		Retrieval:    retrievalResult,
		ToolDefs:     nil,
		ExecutorName: "rag_executor",
		AnswerMode:   answerModeKnowledgeOnly,
	}
	if len(retrievalResult.Hits) == 0 {
		result.Reply = noKnowledgeReply
		return result, nil
	}

	messages := make([]tools.Message, 0, 3+len(history)+1)
	messages = append(messages, tools.Message{Role: "system", Content: e.agent.buildSystemPrompt(ctx, uctx)})
	if prompt := buildKnowledgeOnlyPrompt(retrievalResult); prompt != "" {
		messages = append(messages, tools.Message{Role: "system", Content: prompt})
	}
	messages = append(messages, history...)
	messages = append(messages, tools.Message{Role: "user", Content: question})

	llmCtx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := e.agent.llmClient.Chat(llmCtx, messages, nil)
	result.LLMDuration = elapsedMs(start)
	if err != nil {
		return routeExecutionResult{}, err
	}
	reply := resp.Content
	if reply == "" {
		reply = noKnowledgeReply
	}
	result.Reply = reply
	return result, nil
}

type toolQueryExecutor struct {
	agent *Agent
}

// Execute runs the current logic and returns the normalized result.
func (e toolQueryExecutor) Execute(ctx context.Context, uctx *tools.UserContext, history []tools.Message, question string) (routeExecutionResult, []string, error) {
	if e.agent == nil || e.agent.llmClient == nil || e.agent.registry == nil {
		return routeExecutionResult{}, nil, fmt.Errorf("tool query executor unavailable")
	}

	pool := selectToolPool(question, uctx.UserRole)
	toolDefs := e.agent.registry.ToToolDefsByName(uctx.UserRole, pool.ToolNames)
	messages := make([]tools.Message, 0, 2+len(history))
	messages = append(messages, tools.Message{Role: "system", Content: e.agent.buildSystemPrompt(ctx, uctx)})
	messages = append(messages, history...)
	messages = append(messages, tools.Message{Role: "user", Content: question})

	result := routeExecutionResult{
		ToolDefs:     append([]tools.ToolDef(nil), toolDefs...),
		ExecutorName: "tool_query_executor",
		ToolPool:     pool.Name,
		AnswerMode:   answerModeToolFirst,
	}
	var toolsCalled []string

	for round := 0; round < maxReactRounds; round++ {
		llmTimeout := 50 * time.Second
		if len(messages) > 0 && messages[len(messages)-1].Role == "tool" {
			llmTimeout = 90 * time.Second
		}

		llmCtx, cancel := context.WithTimeout(context.Background(), llmTimeout)
		llmStart := time.Now()
		resp, err := e.agent.llmClient.Chat(llmCtx, messages, toolDefs)
		result.LLMDuration += elapsedMs(llmStart)
		cancel()
		if err != nil {
			return routeExecutionResult{}, toolsCalled, err
		}

		if len(resp.ToolCalls) == 0 {
			reply := resp.Content
			if reply == "" {
				reply = "抱歉，我无法理解您的问题，请换个方式描述"
			}
			result.Reply = reply
			return result, toolsCalled, nil
		}

		messages = append(messages, resp)
		for _, tc := range resp.ToolCalls {
			toolsCalled = append(toolsCalled, tc.Function.Name)
			toolResult, err := e.agent.registry.Dispatch(ctx, uctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			if err != nil {
				toolResult = fmt.Sprintf(`{"error": "工具执行失败: %s"}`, err.Error())
			}

			messages = append(messages, tools.Message{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
			})
		}
	}

	return routeExecutionResult{}, toolsCalled, fmt.Errorf("超出最大轮数")
}

type taskStartExecutor struct {
	agent *Agent
}

// Execute runs the current logic and returns the normalized result.
func (e taskStartExecutor) Execute(ctx context.Context, decision RouteDecision, question string, uctx *tools.UserContext) (taskExecutionResult, error) {
	if e.agent == nil || e.agent.taskCatalog == nil {
		return taskExecutionResult{}, fmt.Errorf("task start executor unavailable")
	}

	task, apply, err := e.agent.taskCatalog.Start(decision.TargetTaskType, question, uctx)
	if err != nil {
		return taskExecutionResult{}, err
	}

	result, err := executeTaskLifecycle(ctx, e.agent, task, uctx, "task_start_executor")
	if err != nil {
		return taskExecutionResult{}, err
	}
	result.MatchedSlots = append([]string(nil), apply.MatchedSlots...)
	if decision.SwitchTask {
		result.Reply = composeSoftTaskNotice(decision.SoftNoticeCode, result.Reply)
	}
	return result, nil
}

type taskContinueExecutor struct {
	agent *Agent
}

// Execute runs the current logic and returns the normalized result.
func (e taskContinueExecutor) Execute(ctx context.Context, task *TaskInstance, question string, uctx *tools.UserContext) (taskExecutionResult, error) {
	if e.agent == nil || e.agent.taskCatalog == nil {
		return taskExecutionResult{}, fmt.Errorf("task continue executor unavailable")
	}

	nextTask, apply, err := e.agent.taskCatalog.Continue(task, question, uctx)
	if err != nil {
		return taskExecutionResult{}, err
	}

	result, err := executeTaskLifecycle(ctx, e.agent, nextTask, uctx, "task_continue_executor")
	if err != nil {
		return taskExecutionResult{}, err
	}
	result.MatchedSlots = append([]string(nil), apply.MatchedSlots...)
	return result, nil
}

type taskMetaExecutor struct {
	agent *Agent
}

// Execute runs the current logic and returns the normalized result.
func (e taskMetaExecutor) Execute(ctx context.Context, task *TaskInstance) (taskExecutionResult, error) {
	if e.agent == nil || e.agent.runtime == nil {
		return taskExecutionResult{}, fmt.Errorf("task meta executor unavailable")
	}
	handler, dispatch := e.agent.runtime.resolveRuntimeHandler(task)
	if dispatch.FallbackReason != "" {
		return taskExecutionResult{}, errors.New(dispatch.FallbackReason)
	}

	toolsCalled, err := handler.Prepare(ctx, task, e.agent.deps)
	if err != nil {
		return taskExecutionResult{}, err
	}

	reply := ""
	if e.agent.taskCatalog != nil {
		reply = e.agent.taskCatalog.BuildMetaReply(task)
	}
	if reply == "" {
		if catalogHandler, ok := e.agent.runtime.resolveCatalogHandler(task.Type); ok {
			reply = catalogHandler.BuildMetaReply(task)
		}
	}
	if reply == "" {
		reply = handler.BuildClarifyReply(task)
	}

	return taskExecutionResult{
		Reply:        reply,
		Task:         cloneTaskInstance(task),
		KeepTaskOpen: true,
		ToolsCalled:  toolsCalled,
		ExecutorName: "task_meta_executor",
		AnswerMode:   answerModeToolFirst,
	}, nil
}

type taskCancelExecutor struct{}

// Execute runs the current logic and returns the normalized result.
func (taskCancelExecutor) Execute(_ *TaskInstance) taskExecutionResult {
	return taskExecutionResult{
		Reply:        "已取消当前任务。如需继续，请重新告诉我。",
		KeepTaskOpen: false,
		ExecutorName: "task_cancel_executor",
		AnswerMode:   answerModeToolFirst,
	}
}

// executeTaskLifecycle runs the shared task lifecycle used by route task executors.
func executeTaskLifecycle(ctx context.Context, agent *Agent, task *TaskInstance, uctx *tools.UserContext, executorName string) (taskExecutionResult, error) {
	if agent == nil || agent.runtime == nil {
		return taskExecutionResult{}, fmt.Errorf("task runtime unavailable")
	}
	handler, dispatch := agent.runtime.resolveRuntimeHandler(task)
	if dispatch.FallbackReason != "" {
		return taskExecutionResult{}, errors.New(dispatch.FallbackReason)
	}

	if task.Status == string(taskStatusReady) {
		taskResult, toolsCalled, err := handler.Execute(ctx, task, uctx, agent.registry)
		if err != nil {
			return taskExecutionResult{}, err
		}
		if taskResult.KeepTaskOpen {
			return taskExecutionResult{
				Reply:        taskResult.Reply,
				Task:         cloneTaskInstance(task),
				KeepTaskOpen: true,
				ToolsCalled:  toolsCalled,
				ExecutorName: executorName,
				AnswerMode:   answerModeToolFirst,
			}, nil
		}
		return taskExecutionResult{
			Reply:        taskResult.Reply,
			Task:         nil,
			KeepTaskOpen: false,
			ToolsCalled:  toolsCalled,
			ExecutorName: executorName,
			AnswerMode:   answerModeToolFirst,
		}, nil
	}

	toolsCalled, err := handler.Prepare(ctx, task, agent.deps)
	if err != nil {
		return taskExecutionResult{}, err
	}

	reply := handler.BuildClarifyReply(task)
	return taskExecutionResult{
		Reply:        reply,
		Task:         cloneTaskInstance(task),
		KeepTaskOpen: true,
		ToolsCalled:  toolsCalled,
		ExecutorName: executorName,
		AnswerMode:   answerModeToolFirst,
	}, nil
}
