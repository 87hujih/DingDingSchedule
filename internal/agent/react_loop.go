package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"schedule_server/internal/agent/tools"
)

// reactLoopResult holds the result of a ReAct loop execution.
type reactLoopResult struct {
	Reply       string
	ToolsCalled []string
	LLMDuration int64
	Rounds      int
}

// toolCallHook is called for each tool invocation during the ReAct loop.
type toolCallHook func(toolName string, args string)

// runReactLoop executes the shared ReAct loop: LLM call -> tool dispatch -> append messages -> repeat.
// It returns the final reply, the list of tools called, and the total LLM duration.
// The optional onToolCall hook is invoked before each tool dispatch (pass nil to skip).
func runReactLoop(
	ctx context.Context,
	llmClient *LLMClient,
	registry *tools.Registry,
	uctx *tools.UserContext,
	messages []tools.Message,
	toolDefs []tools.ToolDef,
	onToolCall toolCallHook,
) (reactLoopResult, error) {
	var toolsCalled []string
	var llmDuration int64

	for round := 0; round < maxReactRounds; round++ {
		// 总结阶段（末尾为 tool 消息）LLM 需处理完整工具结果，输入 token 较多，给予更长超时时间
		llmTimeout := 50 * time.Second
		if len(messages) > 0 && messages[len(messages)-1].Role == "tool" {
			llmTimeout = 90 * time.Second
		}

		llmCtx, cancel := context.WithTimeout(context.Background(), llmTimeout)
		llmStart := time.Now()
		resp, err := llmClient.Chat(llmCtx, messages, toolDefs)
		llmDuration += elapsedMs(llmStart)
		cancel()
		if err != nil {
			return reactLoopResult{LLMDuration: llmDuration, ToolsCalled: toolsCalled, Rounds: round}, err
		}

		// 无工具调用 -> 返回最终回复
		if len(resp.ToolCalls) == 0 {
			reply := resp.Content
			if reply == "" {
				reply = "抱歉，我无法理解您的问题，请换个方式描述"
			}
			return reactLoopResult{
				Reply:       reply,
				ToolsCalled: toolsCalled,
				LLMDuration: llmDuration,
				Rounds:      round + 1,
			}, nil
		}

		// 有工具调用 -> 执行工具
		messages = append(messages, resp)
		for _, tc := range resp.ToolCalls {
			if onToolCall != nil {
				onToolCall(tc.Function.Name, tc.Function.Arguments)
			}
			toolsCalled = append(toolsCalled, tc.Function.Name)
			toolResult, err := registry.Dispatch(ctx, uctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
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

	return reactLoopResult{ToolsCalled: toolsCalled, LLMDuration: llmDuration, Rounds: maxReactRounds}, fmt.Errorf("超出最大轮数")
}
