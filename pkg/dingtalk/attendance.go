package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 钉钉考勤：请假状态查询接口（旧版 OpenAPI）
// 文档名常见为：获取用户请假状态（topapi/attendance/getleavestatus）
const (
	getLeaveStatusURL = "https://oapi.dingtalk.com/topapi/attendance/getleavestatus"
)

// LeaveRecord 统一的请假记录结构（尽量填充，字段依赖钉钉返回）
type LeaveRecord struct {
	DingUserID string    `json:"ding_user_id"`
	LeaveType  string    `json:"leave_type"`
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`

	// 以下为可选 rich 字段，钉钉未返回则为空/0
	DurationSeconds int64  `json:"duration_seconds,omitempty"`
	Status          string `json:"status,omitempty"`
	Remark          string `json:"remark,omitempty"`
}

type leaveStatusResponse struct {
	ErrCode int             `json:"errcode"`
	ErrMsg  string          `json:"errmsg"`
	Result  json.RawMessage `json:"result"`
}

// leaveStatusItem 尽量兼容多种字段命名/类型（常见是毫秒时间戳）
type leaveStatusItem struct {
	UserID  string `json:"userid"`
	UserID2 string `json:"userId"`

	LeaveType  interface{} `json:"leave_type"`
	LeaveType2 interface{} `json:"leaveType"`

	// 常见字段：start_time/end_time（毫秒）
	StartTime  int64 `json:"start_time"`
	StartTime2 int64 `json:"startTime"`
	EndTime    int64 `json:"end_time"`
	EndTime2   int64 `json:"endTime"`

	// 兼容其他可能字段名（少数文档用 begin_time/finish_time）
	BeginTime   int64 `json:"begin_time"`
	BeginTime2  int64 `json:"beginTime"`
	FinishTime  int64 `json:"finish_time"`
	FinishTime2 int64 `json:"finishTime"`

	// 可选字段（不同版本可能有）
	Duration     int64  `json:"duration"`
	DurationUnit string `json:"duration_unit"`
	Status       string `json:"status"`
	Remark       string `json:"remark"`
}

// GetLeaveStatus 查询一批用户在时间窗口内的请假记录（会做分页循环）。
// - userIDs：钉钉 userid 列表
// - startAt/endAt：查询窗口（会转换为毫秒时间戳）
//
// 注意：钉钉接口存在 userIDs 数量限制，内部会对 userIDs 分片调用并合并结果。
func (c *Client) GetLeaveStatus(ctx context.Context, userIDs []string, startAt, endAt time.Time) ([]LeaveRecord, error) {
	userIDs = trimStrings(userIDs)
	if len(userIDs) == 0 {
		return []LeaveRecord{}, nil
	}
	if endAt.Before(startAt) {
		return nil, fmt.Errorf("钉钉请假查询失败: 时间范围无效")
	}

	// 经验值：多数 topapi 支持一次 50/100 个 userid；这里保守按 50 分片
	const chunkSize = 50

	all := make([]LeaveRecord, 0)
	for i := 0; i < len(userIDs); i += chunkSize {
		j := i + chunkSize
		if j > len(userIDs) {
			j = len(userIDs)
		}

		part, err := c.getLeaveStatusByChunk(ctx, userIDs[i:j], startAt, endAt)
		if err != nil {
			return nil, err
		}
		all = append(all, part...)
	}

	return all, nil
}

// isTokenInvalidError 判断是否为 Token 无效错误（需要刷新重试）
func isTokenInvalidError(code int) bool {
	// 200003: 无效的access_token
	// 40014: 不合法的access_token
	// 42001: access_token超时
	return code == 200003 || code == 40014 || code == 42001
}

func (c *Client) getLeaveStatusByChunk(ctx context.Context, userIDs []string, startAt, endAt time.Time) ([]LeaveRecord, error) {
	return c.getLeaveStatusByChunkWithRetry(ctx, userIDs, startAt, endAt, false)
}

func (c *Client) getLeaveStatusByChunkWithRetry(ctx context.Context, userIDs []string, startAt, endAt time.Time, isRetry bool) ([]LeaveRecord, error) {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("钉钉请假查询失败: 获取AccessToken失败: %w", err)
	}

	url := fmt.Sprintf("%s?access_token=%s", getLeaveStatusURL, token)

	// 钉钉该接口常见分页参数为 offset/size；部分版本可能支持 cursor/has_more。
	// 这里实现 offset/size 循环，并兼容 result.has_more/result.next_cursor。
	const pageSize = 50
	var offset int64 = 0

	startMS := startAt.UnixMilli()
	endMS := endAt.UnixMilli()

	records := make([]LeaveRecord, 0)

	for {
		reqBody := map[string]interface{}{
			// 兼容不同参数名/类型：既传数组，也传逗号分隔字符串
			"userids":     userIDs,
			"userid_list": strings.Join(userIDs, ","),
			"start_time":  startMS,
			"end_time":    endMS,
			"offset":      offset,
			"cursor":      offset,
			"size":        pageSize,
		}

		respBody, err := c.postJSON(ctx, url, reqBody)
		if err != nil {
			return nil, fmt.Errorf("钉钉请假查询失败: 请求发送失败: %w", err)
		}

		var resp leaveStatusResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, fmt.Errorf("钉钉请假查询失败: 解析响应失败: %w", err)
		}

		if resp.ErrCode != 0 {
			// Token 无效时，强制刷新并重试一次
			if !isRetry && isTokenInvalidError(resp.ErrCode) {
				c.InvalidateToken()
				return c.getLeaveStatusByChunkWithRetry(ctx, userIDs, startAt, endAt, true)
			}
			return nil, fmt.Errorf("钉钉请假查询失败: code=%d, msg=%s", resp.ErrCode, resp.ErrMsg)
		}

		hasMore, nextCursor, items, err := parseLeaveStatusResult(resp.Result)
		if err != nil {
			return nil, fmt.Errorf("钉钉请假查询失败: 解析result失败: %w", err)
		}

		batch := make([]LeaveRecord, 0, len(items))
		for _, it := range items {
			userID := it.UserID
			if userID == "" {
				userID = it.UserID2
			}

			start := it.StartTime
			if start == 0 {
				start = it.StartTime2
			}
			end := it.EndTime
			if end == 0 {
				end = it.EndTime2
			}

			if start == 0 && it.BeginTime != 0 {
				start = it.BeginTime
			}
			if start == 0 && it.BeginTime2 != 0 {
				start = it.BeginTime2
			}
			if end == 0 && it.FinishTime != 0 {
				end = it.FinishTime
			}
			if end == 0 && it.FinishTime2 != 0 {
				end = it.FinishTime2
			}

			// 兜底：若时间戳为空则跳过
			if start == 0 || end == 0 {
				continue
			}

			leaveType := ""
			rawLeaveType := it.LeaveType
			if rawLeaveType == nil {
				rawLeaveType = it.LeaveType2
			}
			switch v := rawLeaveType.(type) {
			case string:
				leaveType = v
			case float64:
				// JSON 数字默认 float64
				leaveType = fmt.Sprintf("%.0f", v)
			case int:
				leaveType = fmt.Sprintf("%d", v)
			case int64:
				leaveType = fmt.Sprintf("%d", v)
			default:
				leaveType = ""
			}

			rec := LeaveRecord{
				DingUserID: userID,
				LeaveType:  leaveType,
				StartAt:    time.UnixMilli(start),
				EndAt:      time.UnixMilli(end),
				Status:     it.Status,
				Remark:     it.Remark,
			}

			// duration：若返回单位为秒/分钟/小时，尽量换算为秒；否则原样当秒
			if it.Duration > 0 {
				switch it.DurationUnit {
				case "second", "seconds", "s":
					rec.DurationSeconds = it.Duration
				case "minute", "minutes", "m":
					rec.DurationSeconds = it.Duration * 60
				case "hour", "hours", "h":
					rec.DurationSeconds = it.Duration * 3600
				default:
					rec.DurationSeconds = it.Duration
				}
			}

			batch = append(batch, rec)
		}

		records = append(records, batch...)

		// 分页判断：优先 has_more+next_cursor，其次按返回条数推断 offset
		if hasMore {
			// 有些实现用 next_cursor，有些继续用 offset
			if nextCursor > 0 && nextCursor != offset {
				offset = nextCursor
			} else {
				offset += pageSize
			}
			continue
		}

		// 没有 has_more 字段时，用返回条数判断是否继续
		if len(items) >= pageSize {
			offset += pageSize
			continue
		}

		break
	}

	return records, nil
}

func parseLeaveStatusResult(raw json.RawMessage) (hasMore bool, nextCursor int64, items []leaveStatusItem, err error) {
	// 优先解析 snake_case
	var snake struct {
		HasMore    bool              `json:"has_more"`
		NextCursor int64             `json:"next_cursor"`
		Items      []leaveStatusItem `json:"leave_status"`
	}
	if err := json.Unmarshal(raw, &snake); err == nil {
		if snake.HasMore || snake.NextCursor != 0 || snake.Items != nil {
			return snake.HasMore, snake.NextCursor, snake.Items, nil
		}
	}

	// 再解析 camelCase
	var camel struct {
		HasMore    bool              `json:"hasMore"`
		NextCursor int64             `json:"nextCursor"`
		Items      []leaveStatusItem `json:"leaveStatus"`
	}
	if err := json.Unmarshal(raw, &camel); err == nil {
		return camel.HasMore, camel.NextCursor, camel.Items, nil
	}

	// 最后兜底：返回空
	return false, 0, []leaveStatusItem{}, nil
}

func trimStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ========== 打卡记录查询 ==========

const (
	// 获取打卡记录接口
	getAttendanceRecordURL = "https://oapi.dingtalk.com/attendance/listRecord"
)

// CheckRecord 打卡记录
type CheckRecord struct {
	DingUserID string    `json:"ding_user_id"` // 钉钉用户ID
	CheckTime  time.Time `json:"check_time"`   // 打卡时间
	CheckType  string    `json:"check_type"`   // 打卡类型: OnDuty/OffDuty
}

type attendanceRecordResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	Result  struct {
		RecordResult []attendanceRecordItem `json:"recordresult"`
	} `json:"result"`
}

type attendanceRecordItem struct {
	UserID        string `json:"userId"`
	UserID2       string `json:"userid"` // 兼容不同命名
	CheckType     string `json:"checkType"`
	UserCheckTime int64  `json:"userCheckTime"` // 毫秒时间戳
}

// GetAttendanceRecords 获取打卡记录
// userIDs: 钉钉用户ID列表
// startAt/endAt: 查询时间范围
func (c *Client) GetAttendanceRecords(ctx context.Context, userIDs []string, startAt, endAt time.Time) ([]CheckRecord, error) {
	userIDs = trimStrings(userIDs)
	if len(userIDs) == 0 {
		return []CheckRecord{}, nil
	}
	if endAt.Before(startAt) {
		return nil, fmt.Errorf("钉钉打卡查询失败: 时间范围无效")
	}

	// 钉钉API限制：每次最多查询50个用户
	const chunkSize = 50

	all := make([]CheckRecord, 0)
	for i := 0; i < len(userIDs); i += chunkSize {
		j := i + chunkSize
		if j > len(userIDs) {
			j = len(userIDs)
		}

		part, err := c.getAttendanceRecordsByChunk(ctx, userIDs[i:j], startAt, endAt)
		if err != nil {
			return nil, err
		}
		all = append(all, part...)
	}

	return all, nil
}

// getAttendanceRecordsByChunk 分批获取打卡记录
func (c *Client) getAttendanceRecordsByChunk(ctx context.Context, userIDs []string, startAt, endAt time.Time) ([]CheckRecord, error) {
	return c.getAttendanceRecordsByChunkWithRetry(ctx, userIDs, startAt, endAt, false)
}

func (c *Client) getAttendanceRecordsByChunkWithRetry(ctx context.Context, userIDs []string, startAt, endAt time.Time, isRetry bool) ([]CheckRecord, error) {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("钉钉打卡查询失败: 获取AccessToken失败: %w", err)
	}

	url := fmt.Sprintf("%s?access_token=%s", getAttendanceRecordURL, token)

	// 按照钉钉官方文档格式
	reqBody := map[string]interface{}{
		"userIds":       userIDs, // 数组格式
		"checkDateFrom": startAt.Format("2006-01-02 15:04:05"),
		"checkDateTo":   endAt.Format("2006-01-02 15:04:05"),
		"isI18n":        "false", // 字符串 "false"
	}

	respBody, err := c.postJSON(ctx, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("钉钉打卡查询失败: 请求发送失败: %w", err)
	}

	var resp attendanceRecordResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("钉钉打卡查询失败: 解析响应失败: %w", err)
	}

	if resp.ErrCode != 0 {
		// Token 无效时，强制刷新并重试一次
		if !isRetry && isTokenInvalidError(resp.ErrCode) {
			c.InvalidateToken()
			return c.getAttendanceRecordsByChunkWithRetry(ctx, userIDs, startAt, endAt, true)
		}
		return nil, fmt.Errorf("钉钉打卡查询失败: code=%d, msg=%s", resp.ErrCode, resp.ErrMsg)
	}

	records := make([]CheckRecord, 0, len(resp.Result.RecordResult))
	for _, r := range resp.Result.RecordResult {
		userID := r.UserID
		if userID == "" {
			userID = r.UserID2
		}
		if userID == "" || r.UserCheckTime == 0 {
			continue
		}

		records = append(records, CheckRecord{
			DingUserID: userID,
			CheckTime:  time.UnixMilli(r.UserCheckTime),
			CheckType:  r.CheckType,
		})
	}

	return records, nil
}
