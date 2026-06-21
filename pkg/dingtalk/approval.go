package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 钉钉审批：获取审批实例详情
// 文档名常见为：获取审批实例详情（topapi/processinstance/get）
const (
	getProcessInstanceURL = "https://oapi.dingtalk.com/topapi/processinstance/get"
)

// ProcessInstanceFormComponentValue 审批表单字段值
type ProcessInstanceFormComponentValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ProcessInstance 审批实例详情（按你们落库所需做了归一化）
type ProcessInstance struct {
	ProcessInstanceID string `json:"process_instance_id"`
	ProcessCode       string `json:"process_code,omitempty"`
	Title             string `json:"title,omitempty"`
	Status            string `json:"status,omitempty"` // RUNNING/COMPLETED/TERMINATED...
	Result            string `json:"result,omitempty"` // agree/refuse/...

	OriginatorUserID string                              `json:"originator_user_id,omitempty"`
	FormValues       []ProcessInstanceFormComponentValue `json:"form_component_values,omitempty"`

	// Raw 原始审批实例 JSON（便于后续排障/字段映射）
	Raw json.RawMessage `json:"-"`
}

type processInstanceGetResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	// 不同版本字段可能不同，尽量兼容
	ProcessInstance  json.RawMessage `json:"process_instance"`
	ProcessInstance2 json.RawMessage `json:"processInstance"`
	Result           json.RawMessage `json:"result"`
}

type processInstancePayload struct {
	ProcessInstanceID  string `json:"process_instance_id"`
	ProcessInstanceID2 string `json:"processInstanceId"`

	ProcessCode  string `json:"process_code"`
	ProcessCode2 string `json:"processCode"`

	BusinessID string `json:"business_id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Result     string `json:"result"`

	OriginatorUserID  string `json:"originator_userid"`
	OriginatorUserID2 string `json:"originator_user_id"`
	OriginatorUserID3 string `json:"originatorUserId"`

	FormComponentValues  []ProcessInstanceFormComponentValue `json:"form_component_values"`
	FormComponentValues2 []ProcessInstanceFormComponentValue `json:"formComponentValues"`
}

func (p processInstancePayload) toProcessInstance(raw json.RawMessage) *ProcessInstance {
	id := strings.TrimSpace(p.ProcessInstanceID)
	if id == "" {
		id = strings.TrimSpace(p.ProcessInstanceID2)
	}
	code := strings.TrimSpace(p.ProcessCode)
	if code == "" {
		code = strings.TrimSpace(p.ProcessCode2)
	}
	originator := strings.TrimSpace(p.OriginatorUserID)
	if originator == "" {
		originator = strings.TrimSpace(p.OriginatorUserID2)
	}
	if originator == "" {
		originator = strings.TrimSpace(p.OriginatorUserID3)
	}
	form := p.FormComponentValues
	if len(form) == 0 {
		form = p.FormComponentValues2
	}

	return &ProcessInstance{
		ProcessInstanceID: id,
		ProcessCode:       code,
		Title:             p.Title,
		Status:            p.Status,
		Result:            p.Result,
		OriginatorUserID:  originator,
		FormValues:        form,
		Raw:               raw,
	}
}

// GetProcessInstance 获取审批实例详情。
// - processInstanceID：审批实例ID（每次提交生成一个新的实例ID）
// 注意：该接口是旧版 oapi 域名（access_token query 参数），与你们当前用户/考勤接口一致。
func (c *Client) GetProcessInstance(ctx context.Context, processInstanceID string) (*ProcessInstance, error) {
	processInstanceID = strings.TrimSpace(processInstanceID)
	if processInstanceID == "" {
		return nil, fmt.Errorf("钉钉审批实例查询失败: process_instance_id 为空")
	}

	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("钉钉审批实例查询失败: 获取AccessToken失败: %w", err)
	}

	url := fmt.Sprintf("%s?access_token=%s", getProcessInstanceURL, token)
	reqBody := map[string]string{"process_instance_id": processInstanceID}

	respBody, err := c.postJSON(ctx, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("钉钉审批实例查询失败: 请求发送失败: %w", err)
	}

	var resp processInstanceGetResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("钉钉审批实例查询失败: 解析响应失败: %w, body=%s", err, string(respBody))
	}
	if resp.ErrCode != 0 {
		return nil, fmt.Errorf("钉钉审批实例查询失败: code=%d, msg=%s, processInstanceId=%s", resp.ErrCode, resp.ErrMsg, processInstanceID)
	}

	raw := resp.ProcessInstance
	if len(raw) == 0 {
		raw = resp.ProcessInstance2
	}
	if len(raw) == 0 {
		raw = resp.Result
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("钉钉审批实例查询失败: 响应缺少 process_instance/result, body=%s", string(respBody))
	}

	var payload processInstancePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("钉钉审批实例查询失败: 解析实例失败: %w, raw=%s", err, string(raw))
	}

	pi := payload.toProcessInstance(raw)
	// 钉钉响应中可能不包含 process_instance_id，使用请求参数中的 ID
	if pi.ProcessInstanceID == "" {
		pi.ProcessInstanceID = processInstanceID
	}
	return pi, nil
}


