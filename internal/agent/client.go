package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"schedule_server/internal/agent/tools"
)

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
		httpClient: &http.Client{
			Timeout: 50 * time.Second,
		},
	}
}

// chatRequest OpenAI chat completion 请求体
type chatRequest struct {
	Model    string          `json:"model"`
	Messages []msgJSON       `json:"messages"`
	Tools    []tools.ToolDef `json:"tools,omitempty"`
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
	msgs := make([]msgJSON, 0, len(messages))
	for _, m := range messages {
		msg := msgJSON{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			ToolCalls:  m.ToolCalls,
		}
		msgs = append(msgs, msg)
	}

	reqBody := chatRequest{
		Model:    c.model,
		Messages: msgs,
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

func (c *LLMClient) doChat(ctx context.Context, reqBody chatRequest) (tools.Message, bool, error) {
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
		return tools.Message{}, true, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return tools.Message{}, true, fmt.Errorf("读取响应失败: %w", err)
	}

	// 429 或 5xx 可重试
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return tools.Message{}, true, fmt.Errorf("LLM API 返回 %d: %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return tools.Message{}, false, fmt.Errorf("LLM API 返回 %d: %s", resp.StatusCode, string(respBody))
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
	return tools.Message{
		Role:      choice.Message.Role,
		Content:   choice.Message.Content,
		ToolCalls: choice.Message.ToolCalls,
	}, false, nil
}
