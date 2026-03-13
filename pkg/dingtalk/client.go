package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// 钉钉获取 AccessToken 的接口（旧版API，兼容 oapi.dingtalk.com 的接口）
	tokenURL = "https://oapi.dingtalk.com/gettoken"
	// 提前刷新时间（Token 过期前 5 分钟刷新）
	refreshAdvance    = 5 * time.Minute
	sendWorkNoticeURL = "https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2"
)

var (
	ErrEmptyCredentials = errors.New("钉钉: appKey 或 appSecret 为空")
	ErrTokenFetch       = errors.New("钉钉: 获取 AccessToken 失败")
	ErrTokenInvalid     = errors.New("钉钉: AccessToken 无效")
)

// tokenResponse 钉钉获取 Token 的响应结构（旧API格式）
type tokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"` // 旧API使用 access_token
	ExpiresIn   int64  `json:"expires_in"`   // 旧API使用 expires_in，单位秒
}

// Client 钉钉客户端，自动管理 AccessToken
type Client struct {
	appKey    string
	appSecret string

	mu          sync.RWMutex
	accessToken string
	expireAt    time.Time // Token 过期时间

	httpClient *http.Client
}

// NewClient 创建钉钉客户端
func NewClient(appKey, appSecret string) (*Client, error) {
	if appKey == "" || appSecret == "" {
		return nil, ErrEmptyCredentials
	}
	return &Client{
		appKey:    appKey,
		appSecret: appSecret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

// GetAccessToken 获取有效的 AccessToken（自动刷新）
func (c *Client) GetAccessToken(ctx context.Context) (string, error) {
	// 先尝试读取缓存的 Token
	c.mu.RLock()
	token := c.accessToken
	expireAt := c.expireAt
	c.mu.RUnlock()

	// 如果 Token 有效且未过期（提前 5 分钟刷新）
	if token != "" && time.Now().Add(refreshAdvance).Before(expireAt) {
		return token, nil
	}

	// 需要刷新 Token
	return c.refreshToken(ctx)
}

// InvalidateToken 使当前 Token 失效，强制下次请求时刷新
func (c *Client) InvalidateToken() {
	c.mu.Lock()
	c.accessToken = ""
	c.expireAt = time.Time{}
	c.mu.Unlock()
}

// refreshToken 从钉钉服务器获取新的 AccessToken
func (c *Client) refreshToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 双重检查：可能其他 goroutine 已经刷新过了
	if c.accessToken != "" && time.Now().Add(refreshAdvance).Before(c.expireAt) {
		return c.accessToken, nil
	}

	// 旧API使用 GET 请求，参数在 URL 中
	url := fmt.Sprintf("%s?appkey=%s&appsecret=%s", tokenURL, c.appKey, c.appSecret)

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("钉钉: 创建请求失败: %w", err)
	}

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("钉钉: 发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("钉钉: 读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: 状态码=%d, 响应=%s", ErrTokenFetch, resp.StatusCode, string(respBody))
	}

	// 解析响应
	var tokenResp tokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("钉钉: 解析响应失败: %w", err)
	}

	// 检查钉钉API错误码
	if tokenResp.ErrCode != 0 {
		return "", fmt.Errorf("%w: errcode=%d, errmsg=%s", ErrTokenFetch, tokenResp.ErrCode, tokenResp.ErrMsg)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("%w: 响应中 Token 为空, 响应=%s", ErrTokenFetch, string(respBody))
	}

	// 更新缓存
	c.accessToken = tokenResp.AccessToken
	c.expireAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return c.accessToken, nil
}

// Request 发送带 AccessToken 的钉钉 API 请求
func (c *Client) Request(ctx context.Context, method, url string, body any) ([]byte, error) {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("钉钉: 获取 AccessToken 失败: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("钉钉: 序列化请求体失败: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("钉钉: 创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("钉钉: 发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("钉钉: 读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("钉钉: API 调用失败: 状态码=%d, 响应=%s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Get 发送 GET 请求
func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	return c.Request(ctx, http.MethodGet, url, nil)
}

// Post 发送 POST 请求
func (c *Client) Post(ctx context.Context, url string, body any) ([]byte, error) {
	return c.Request(ctx, http.MethodPost, url, body)
}

type workNoticeResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	TaskID  int64  `json:"task_id"`
}

// SendWorkNoticeText 发送钉钉系统消息
func (c *Client) SendWorkNoticeText(ctx context.Context, agentID string, userIDs []string, content string) error {
	userIDs = trimStrings(userIDs)
	if len(userIDs) == 0 {
		return nil
	}
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("钉钉: agent_id 为空")
	}
	const chunkSize = 100
	for i := 0; i < len(userIDs); i += chunkSize {
		j := i + chunkSize
		if j > len(userIDs) {
			j = len(userIDs)
		}
		if err := c.sendWorkNoticeTextByChunk(ctx, agentID, userIDs[i:j], content, false); err != nil {
			return err
		}
	}
	return nil
}

// SendGroupRobotMessage 机器人主动发文本消息到群聊
func (c *Client) SendGroupRobotMessage(ctx context.Context, robotCode, conversationID, content string) error {
	msgParam, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return fmt.Errorf("钉钉: 序列化 msgParam 失败: %w", err)
	}
	body := map[string]string{
		"robotCode":          robotCode,
		"openConversationId": conversationID,
		"msgKey":             "sampleText",
		"msgParam":           string(msgParam),
	}

	_, err = c.Post(ctx, "https://api.dingtalk.com/v1.0/robot/groupMessages/send", body)
	if err != nil {
		return fmt.Errorf("钉钉: 发送群消息失败: %w", err)
	}
	return nil
}

func (c *Client) sendWorkNoticeTextByChunk(ctx context.Context, agentID string, userIDs []string, content string, isRetry bool) error {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("钉钉: 获取AccessToken失败: %w", err)
	}
	url := fmt.Sprintf("%s?access_token=%s", sendWorkNoticeURL, token)
	reqBody := map[string]interface{}{
		"agent_id":    agentID,
		"userid_list": strings.Join(userIDs, ","),
		"msg": map[string]interface{}{
			"msgtype": "text",
			"text": map[string]string{
				"content": content,
			},
		},
	}
	respBody, err := c.postJSON(ctx, url, reqBody)
	if err != nil {
		return fmt.Errorf("钉钉: 发送工作通知失败: %w", err)
	}

	var resp workNoticeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("钉钉: 解析工作通知响应失败: %w", err)
	}
	if resp.ErrCode != 0 {
		if !isRetry && isTokenInvalidError(resp.ErrCode) {
			c.InvalidateToken()
			return c.sendWorkNoticeTextByChunk(ctx, agentID, userIDs, content, true)
		}
		return fmt.Errorf("钉钉: 工作通知失败: code=%d, msg=%s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}
