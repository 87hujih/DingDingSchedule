package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

// maxResponseBodySize 限制 LLM 响应体最大读取量（10MB），防止异常端点返回超大响应。
const maxResponseBodySize = 10 * 1024 * 1024

// LLMClient OpenAI-compatible HTTP 客户端
type LLMClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewLLMClient 创建 LLM 客户端
func NewLLMClient(baseURL, apiKey, model string) *LLMClient {
	return &LLMClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		// 请求超时由调用方 context 控制，避免固定 HTTP 超时截断总结阶段的更长等待时间。
		httpClient: &http.Client{},
	}
}

// chatRequest OpenAI chat completion 请求体
type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []msgJSON       `json:"messages"`
	Tools          []tools.ToolDef `json:"tools,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

// StructuredOutputSpec defines the bounded request contract used by the intent compiler.
type StructuredOutputSpec struct {
	Mode                 string
	Temperature          float64
	MaxTokens            int
	TransportMaxAttempts int
	ParseRepairAttempts  int
}

// StructuredChatResponse carries the completion and the exact number of HTTP attempts.
type StructuredChatResponse struct {
	Message  tools.Message
	Attempts int
}

type llmHTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *llmHTTPStatusError) Error() string {
	return fmt.Sprintf("LLM API 返回 %d: %s", e.StatusCode, e.Body)
}

// msgJSON 发送给 API 的消息格式
type msgJSON struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []tools.ToolCall `json:"tool_calls,omitempty"`
}

// chatResponse OpenAI chat completion 响应体
type chatResponse struct {
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []tools.ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat 发送对话请求，内置 3 次重试（429/5xx）
func (c *LLMClient) Chat(ctx context.Context, messages []tools.Message, toolDefs []tools.ToolDef) (tools.Message, error) {
	reqBody := chatRequest{
		Model:    c.model,
		Messages: chatMessagesJSON(messages),
	}
	if len(toolDefs) > 0 {
		reqBody.Tools = toolDefs
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return tools.Message{}, ctx.Err()
			case <-time.After(backoff):
			}
		}

		result, retryable, err := c.doChat(ctx, reqBody)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retryable {
			return tools.Message{}, err
		}
	}

	return tools.Message{}, fmt.Errorf("LLM 请求失败（已重试3次）: %w", lastErr)
}

// ChatStructured sends a bounded structured-output request for IntentCompiler only.
// All attempts share the caller context; parsing/repair retries are intentionally unsupported.
func (c *LLMClient) ChatStructured(
	ctx context.Context,
	messages []tools.Message,
	spec StructuredOutputSpec,
) (StructuredChatResponse, error) {
	if spec.ParseRepairAttempts != 0 {
		return StructuredChatResponse{}, errors.New("structured parse repair is not supported")
	}
	if spec.MaxTokens <= 0 {
		return StructuredChatResponse{}, errors.New("structured max tokens must be positive")
	}
	maxAttempts := spec.TransportMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if maxAttempts > 2 {
		return StructuredChatResponse{}, errors.New("structured transport attempts must not exceed 2")
	}

	reqBody := chatRequest{
		Model:       c.model,
		Messages:    chatMessagesJSON(messages),
		Temperature: &spec.Temperature,
		MaxTokens:   &spec.MaxTokens,
	}
	switch strings.TrimSpace(spec.Mode) {
	case "json_object":
		reqBody.ResponseFormat = &responseFormat{Type: "json_object"}
	case "prompt_only":
		// Explicit compatibility mode. It is never selected implicitly.
	default:
		return StructuredChatResponse{}, fmt.Errorf("unsupported structured output mode %q", spec.Mode)
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return StructuredChatResponse{Attempts: attempt - 1}, ctx.Err()
			case <-time.After(time.Second):
			}
		}

		result, retryable, err := c.doChat(ctx, reqBody)
		if err == nil {
			return StructuredChatResponse{Message: result, Attempts: attempt}, nil
		}
		lastErr = err
		if !structuredTransportRetryable(retryable, err) {
			return StructuredChatResponse{Attempts: attempt}, err
		}
	}
	return StructuredChatResponse{Attempts: maxAttempts}, fmt.Errorf(
		"structured LLM request failed after %d attempts: %w",
		maxAttempts,
		lastErr,
	)
}

func chatMessagesJSON(messages []tools.Message) []msgJSON {
	result := make([]msgJSON, 0, len(messages))
	for _, message := range messages {
		result = append(result, msgJSON{
			Role:       message.Role,
			Content:    message.Content,
			ToolCallID: message.ToolCallID,
			ToolCalls:  message.ToolCalls,
		})
	}
	return result
}

func structuredTransportRetryable(retryable bool, err error) bool {
	if !retryable {
		return false
	}
	var statusErr *llmHTTPStatusError
	if !errors.As(err, &statusErr) {
		return true
	}
	switch statusErr.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// doChat sends a chat request to the configured LLM endpoint.
func (c *LLMClient) doChat(ctx context.Context, reqBody chatRequest) (tools.Message, bool, error) {
	// 快速失败：父 context 已超时/取消，无需发起请求
	if ctx.Err() != nil {
		return tools.Message{}, false, ctx.Err()
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return tools.Message{}, false, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return tools.Message{}, false, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// 父 context 超时/取消：重试只会立即再失败
		if ctx.Err() != nil {
			return tools.Message{}, false, fmt.Errorf("发送请求失败: %w", err)
		}
		// HTTP 级超时（httpClient.Timeout）：API 响应过慢，重试大概率同样超时
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return tools.Message{}, false, fmt.Errorf("发送请求失败: %w", err)
		}
		// 其他网络错误（连接重置、DNS 等）：可能是瞬时抖动，值得重试
		return tools.Message{}, true, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
	if err != nil {
		return tools.Message{}, true, fmt.Errorf("读取响应失败: %w", err)
	}

	// 429 或 5xx 可重试
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return tools.Message{}, true, &llmHTTPStatusError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if resp.StatusCode != http.StatusOK {
		return tools.Message{}, false, &llmHTTPStatusError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return tools.Message{}, false, fmt.Errorf("解析响应失败: %w", err)
	}

	if chatResp.Error != nil {
		return tools.Message{}, false, fmt.Errorf("LLM API 错误: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return tools.Message{}, false, fmt.Errorf("LLM API 返回空 choices")
	}

	choice := chatResp.Choices[0]
	result := tools.Message{
		Role:      choice.Message.Role,
		Content:   choice.Message.Content,
		ToolCalls: choice.Message.ToolCalls,
	}

	// DSML 兜底：部分 LLM API 在 function calling 格式转换失败时，会将原生 DSML 工具调用
	// 标记泄漏到 content 字段。检测到时将其解析为标准 ToolCall，并从 content 中移除。
	if len(result.ToolCalls) == 0 && strings.Contains(result.Content, "<｜DSML｜function_calls>") {
		if dsmlCalls, clean := parseDSMLToolCalls(result.Content); len(dsmlCalls) > 0 {
			result.ToolCalls = dsmlCalls
			result.Content = clean
		}
	}

	return result, false, nil
}

// parseDSMLToolCalls 解析 content 中内嵌的 DSML 格式工具调用，返回解析出的 ToolCall
// 列表及去除 DSML 块后的干净 content。
// 格式示例：
//
//	<｜DSML｜function_calls><｜DSML｜invoke name="tool_name">
//	<｜DSML｜function_call><name>tool_name</name>
//	<arguments>{"key": "value"}</arguments>
//	</｜DSML｜function_call></｜DSML｜invoke></｜DSML｜function_calls>
func parseDSMLToolCalls(content string) ([]tools.ToolCall, string) {
	const blockOpen = "<｜DSML｜function_calls>"
	const blockClose = "</｜DSML｜function_calls>"

	bStart := strings.Index(content, blockOpen)
	if bStart == -1 {
		return nil, content
	}
	bEnd := strings.Index(content, blockClose)
	if bEnd == -1 {
		return nil, content
	}
	bEnd += len(blockClose)

	block := content[bStart:bEnd]
	cleanContent := strings.TrimSpace(content[:bStart] + content[bEnd:])

	const invokeOpen = "<｜DSML｜invoke name=\""
	const invokeClose = "</｜DSML｜invoke>"
	const argOpen = "<arguments>"
	const argClose = "</arguments>"

	var toolCalls []tools.ToolCall
	remaining := block
	idSeq := 0

	for {
		iStart := strings.Index(remaining, invokeOpen)
		if iStart == -1 {
			break
		}
		nameStart := iStart + len(invokeOpen)
		nameEnd := strings.Index(remaining[nameStart:], "\"")
		if nameEnd == -1 {
			break
		}
		funcName := remaining[nameStart : nameStart+nameEnd]

		iClose := strings.Index(remaining[iStart:], invokeClose)
		if iClose == -1 {
			break
		}
		invokeBody := remaining[iStart : iStart+iClose+len(invokeClose)]
		remaining = remaining[iStart+iClose+len(invokeClose):]

		// 提取 JSON arguments
		args := "{}"
		aStart := strings.Index(invokeBody, argOpen)
		aEnd := strings.LastIndex(invokeBody, argClose)
		if aStart != -1 && aEnd > aStart {
			args = strings.TrimSpace(invokeBody[aStart+len(argOpen) : aEnd])
		}

		idSeq++
		toolCalls = append(toolCalls, tools.ToolCall{
			ID:   fmt.Sprintf("dsml_%d", idSeq),
			Type: "function",
			Function: tools.FunctionCall{
				Name:      funcName,
				Arguments: args,
			},
		})
	}

	return toolCalls, cleanContent
}
