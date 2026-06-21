package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// 获取子部门列表
	deptListSubURL = "https://oapi.dingtalk.com/topapi/v2/department/listsub"
)

// Department 部门信息
type Department struct {
	DeptID   int64  `json:"dept_id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parent_id"`
}

// DeptListResponse 获取部门列表响应
type DeptListResponse struct {
	ErrCode int          `json:"errcode"`
	ErrMsg  string       `json:"errmsg"`
	Result  []Department `json:"result"`
}

// GetSubDepartments 获取指定部门的子部门列表
// deptID: 父部门ID，根部门传1
func (c *Client) GetSubDepartments(ctx context.Context, deptID int64) ([]Department, error) {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取AccessToken失败: %w", err)
	}

	// 旧版API使用query参数传token
	url := fmt.Sprintf("%s?access_token=%s", deptListSubURL, token)

	reqBody := map[string]any{
		"dept_id": deptID,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var deptResp DeptListResponse
	if err := json.Unmarshal(respBody, &deptResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if deptResp.ErrCode != 0 {
		return nil, fmt.Errorf("钉钉API错误: code=%d, msg=%s", deptResp.ErrCode, deptResp.ErrMsg)
	}

	return deptResp.Result, nil
}

// FetchAllDepartments 递归获取所有部门（直接返回列表，不缓存）
func (c *Client) FetchAllDepartments(ctx context.Context) ([]Department, error) {
	var allDepts []Department

	// 根部门ID为1
	if err := c.fetchDeptRecursive(ctx, 1, &allDepts); err != nil {
		return nil, err
	}

	return allDepts, nil
}

// fetchDeptRecursive 递归获取部门
func (c *Client) fetchDeptRecursive(ctx context.Context, parentID int64, result *[]Department) error {
	// 限流：每次请求间隔60ms，确保不超过15次/秒（钉钉限制20次/秒）
	time.Sleep(60 * time.Millisecond)

	depts, err := c.GetSubDepartments(ctx, parentID)
	if err != nil {
		return fmt.Errorf("获取部门(parentID=%d)子部门失败: %w", parentID, err)
	}

	for _, dept := range depts {
		*result = append(*result, dept)

		// 递归获取子部门
		if err := c.fetchDeptRecursive(ctx, dept.DeptID, result); err != nil {
			return err
		}
	}

	return nil
}
