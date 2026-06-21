package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const (
	// 通过免登码获取用户信息（直接返回userid）
	getUserByCodeURL = "https://oapi.dingtalk.com/user/getuserinfo"
	// 获取用户详情
	getUserDetailURL = "https://oapi.dingtalk.com/topapi/v2/user/get"
)

// 预定义错误
var (
	ErrUserNotFound    = errors.New("dingtalk: 用户不存在")
	ErrAuthCodeInvalid = errors.New("dingtalk: 免登码无效")
)

// UserDetail 用户详细信息（管理后台API获取）
type UserDetail struct {
	UserID     string  `json:"userid"`
	UnionID    string  `json:"unionid"`
	Name       string  `json:"name"`
	Avatar     string  `json:"avatar"`
	Mobile     string  `json:"mobile"`
	Email      string  `json:"email"`
	DeptIDList []int64 `json:"dept_id_list"` // 所属部门ID列表
	Title      string  `json:"title"`        // 职位
	HiredDate  int64   `json:"hired_date"`   // 入职时间
	Active     bool    `json:"active"`       // 是否激活
}

// userDetailResponse 获取用户详情响应
type userDetailResponse struct {
	ErrCode int        `json:"errcode"`
	ErrMsg  string     `json:"errmsg"`
	Result  UserDetail `json:"result"`
}

// authCodeResponse 免登码获取用户信息响应
type authCodeResponse struct {
	ErrCode  int    `json:"errcode"`
	ErrMsg   string `json:"errmsg"`
	UserID   string `json:"userid"`
	DeviceID string `json:"deviceId"`
}

// GetUserByAuthCode 通过免登授权码获取用户信息
// authCode: 前端通过钉钉SDK获取的免登码
// 返回: userid（不再返回UserInfo，因为旧版API只返回userid）
func (c *Client) GetUserByAuthCode(ctx context.Context, authCode string) (string, error) {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("dingtalk: 获取AccessToken失败: %w", err)
	}

	// 旧版API：直接用authCode+企业AccessToken获取userid
	url := fmt.Sprintf("%s?access_token=%s&code=%s", getUserByCodeURL, token, authCode)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("dingtalk: 创建请求失败: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dingtalk: 发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	var result authCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("dingtalk: 解析响应失败: %w", err)
	}

	if result.ErrCode != 0 {
		return "", fmt.Errorf("dingtalk: API错误: code=%d, msg=%s", result.ErrCode, result.ErrMsg)
	}

	return result.UserID, nil
}

// GetUserDetail 获取用户详细信息（包含部门列表）
// userID: 钉钉用户ID
func (c *Client) GetUserDetail(ctx context.Context, userID string) (*UserDetail, error) {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("dingtalk: 获取AccessToken失败: %w", err)
	}

	url := fmt.Sprintf("%s?access_token=%s", getUserDetailURL, token)
	reqBody := map[string]string{"userid": userID}

	respBody, err := c.postJSON(ctx, url, reqBody)
	if err != nil {
		return nil, err
	}

	var resp userDetailResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("dingtalk: 解析响应失败: %w", err)
	}

	if resp.ErrCode != 0 {
		if resp.ErrCode == 60121 { // 用户不存在
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("dingtalk: API错误: code=%d, msg=%s", resp.ErrCode, resp.ErrMsg)
	}

	return &resp.Result, nil
}

// postJSON 内部辅助方法：发送POST JSON请求
func (c *Client) postJSON(ctx context.Context, url string, body any) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("dingtalk: 序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("dingtalk: 创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dingtalk: 发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody := make([]byte, 0, 1024)
	buf := make([]byte, 512)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			respBody = append(respBody, buf[:n]...)
		}
		if readErr != nil {
			break
		}
	}

	return respBody, nil
}
