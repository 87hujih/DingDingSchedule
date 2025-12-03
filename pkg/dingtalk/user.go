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
	// 新版API：通过免登码获取用户信息
	getUserByCodeURL = "https://api.dingtalk.com/v1.0/contact/users/me"
	// 旧版API：获取用户详情
	getUserDetailURL = "https://oapi.dingtalk.com/topapi/v2/user/get"
)

// 预定义错误
var (
	ErrUserNotFound    = errors.New("dingtalk: 用户不存在")
	ErrAuthCodeInvalid = errors.New("dingtalk: 免登码无效")
)

// UserInfo 用户基本信息（免登码获取）
type UserInfo struct {
	Nick      string `json:"nick"`
	UnionID   string `json:"unionId"`
	OpenID    string `json:"openId"`
	AvatarURL string `json:"avatarUrl"`
	Mobile    string `json:"mobile"`
}

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

// GetUserByAuthCode 通过免登授权码获取用户信息
// authCode: 前端通过钉钉SDK获取的免登码
func (c *Client) GetUserByAuthCode(ctx context.Context, authCode string) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getUserByCodeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dingtalk: 创建请求失败: %w", err)
	}

	// 新版API使用Header传递authCode
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", authCode)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dingtalk: 发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthCodeInvalid
	}

	var userInfo UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, fmt.Errorf("dingtalk: 解析响应失败: %w", err)
	}

	return &userInfo, nil
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

// GetUserIDByUnionID 通过unionID获取用户ID
func (c *Client) GetUserIDByUnionID(ctx context.Context, unionID string) (string, error) {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("dingtalk: 获取AccessToken失败: %w", err)
	}

	url := fmt.Sprintf("https://oapi.dingtalk.com/topapi/user/getbyunionid?access_token=%s", token)
	reqBody := map[string]string{"unionid": unionID}

	respBody, err := c.postJSON(ctx, url, reqBody)
	if err != nil {
		return "", err
	}

	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Result  struct {
			UserID string `json:"userid"`
		} `json:"result"`
	}

	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("dingtalk: 解析响应失败: %w", err)
	}

	if resp.ErrCode != 0 {
		if resp.ErrCode == 60121 {
			return "", ErrUserNotFound
		}
		return "", fmt.Errorf("dingtalk: API错误: code=%d, msg=%s", resp.ErrCode, resp.ErrMsg)
	}

	return resp.Result.UserID, nil
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
