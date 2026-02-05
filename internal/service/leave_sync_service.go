package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"schedule_server/internal/errs"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

// LeaveSyncService 请假审批同步服务（从钉钉审批回调中拉取详情并落库）。
type LeaveSyncService struct {
	leaveRepo repository.LeaveApprovalRepository
	userRepo  repository.UserRepository
	dingMgr   *DingTalkClientManager
	logger    *zap.SugaredLogger
}

func NewLeaveSyncService(
	leaveRepo repository.LeaveApprovalRepository,
	userRepo repository.UserRepository,
	dingMgr *DingTalkClientManager,
	logger *zap.SugaredLogger,
) *LeaveSyncService {
	return &LeaveSyncService{
		leaveRepo: leaveRepo,
		userRepo:  userRepo,
		dingMgr:   dingMgr,
		logger:    logger,
	}
}

// SyncProcessInstance 异步同步审批实例（从回调事件中调用）。
//
// 流程：
// 1. 根据 corpID 获取 tenant + dingtalk client
// 2. 调用钉钉 API 获取审批实例详情
// 3. 解析表单字段（start/end/reason/leaveType）
// 4. 查找本地 user_id（通过 ding_user_id）
// 5. Upsert 到 leave_approvals 表
//
// 注意：该方法应在 goroutine 中调用，避免阻塞回调响应。
func (s *LeaveSyncService) SyncProcessInstance(ctx context.Context, corpID, processInstanceID string) error {
	if corpID == "" || processInstanceID == "" {
		return errors.New("leave_sync: corpID 或 processInstanceID 为空")
	}

	// 1. 获取 tenant + client
	tenant, client, err := s.dingMgr.GetByCorpID(ctx, corpID)
	if err != nil {
		s.logger.Errorw("leave_sync: 获取租户/客户端失败",
			"corpID", corpID,
			"processInstanceID", processInstanceID,
			"err", err,
		)
		return errs.WrapMsgErr("leave_sync: 获取租户/客户端失败", err)
	}

	// 2. 拉取审批实例详情
	pi, err := client.GetProcessInstance(ctx, processInstanceID)
	if err != nil {
		s.logger.Errorw("leave_sync: 获取审批实例详情失败",
			"corpID", corpID,
			"tenantID", tenant.ID,
			"processInstanceID", processInstanceID,
			"err", err,
		)
		return errs.WrapMsgErr("leave_sync: 获取审批实例详情失败", err)
	}

	// 3. 解析表单字段（start/end/reason/leaveType）
	startAt, endAt, reason, leaveType, err := s.parseLeaveFormFields(pi.FormValues)
	if err != nil {
		s.logger.Warnw("leave_sync: 解析表单字段失败，使用默认值",
			"processInstanceId", processInstanceID,
			"err", err,
		)
		// 不阻断，继续处理
	}

	// 4. 创建带租户ID的上下文（用于后续数据库操作）
	ctxWithTenant := tenantctx.WithTenantID(ctx, tenant.ID)

	// 5. 查找本地 user_id 和姓名
	dingUserID := strings.TrimSpace(pi.OriginatorUserID)
	var userID uint
	var userName string
	if dingUserID != "" {
		user, err := s.userRepo.FindByDingUserID(ctxWithTenant, dingUserID)
		if err == nil && user != nil {
			userID = user.ID
			userName = user.Name
		}
	}

	// 6. 构建落库记录
	rec := &model.LeaveApproval{
		TenantID:          tenant.ID,
		ProcessInstanceID: pi.ProcessInstanceID,
		ProcessCode:       pi.ProcessCode,
		DingUserID:        dingUserID,
		UserID:            userID,
		UserName:          userName,
		StartAt:           startAt,
		EndAt:             endAt,
		LeaveType:         leaveType,
		Reason:            reason,
		ApproveStatus:     normalizeStatus(pi.Status),
		Result:            normalizeResult(pi.Result),
	}

	// 保留原始 JSON（便于排障）
	if rawBytes, err := json.Marshal(pi.Raw); err == nil {
		rec.RawInstanceJSON = string(rawBytes)
	}
	if formBytes, err := json.Marshal(pi.FormValues); err == nil {
		rec.RawFormJSON = string(formBytes)
	}

	// 7. Upsert（幂等）
	// 注意：使用带租户ID的上下文，确保 tenant_id 被正确注入
	if err := s.leaveRepo.UpsertByProcessInstanceID(ctxWithTenant, rec); err != nil {
		return errs.WrapMsgErr("leave_sync: 落库失败", err)
	}

	s.logger.Infow("leave_sync: 同步成功",
		"tenantId", tenant.ID,
		"processInstanceId", processInstanceID,
		"dingUserId", dingUserID,
		"userId", userID,
		"userName", userName,
		"startAt", startAt,
		"endAt", endAt,
		"status", rec.ApproveStatus,
	)

	return nil
}

// parseLeaveFormFields 从表单字段数组中解析请假时间、理由、类型。
//
// 钉钉请假组件（DDHolidayField）的特殊格式：
// - name: "[\"开始时间\",\"结束时间\"]"
// - value: "[\"2026-01-06 13:51\",\"2026-01-06 13:52\",0.02,\"hour\",\"事假\",\"请假类型\"]"
func (s *LeaveSyncService) parseLeaveFormFields(formValues []dingtalk.ProcessInstanceFormComponentValue) (
	startAt time.Time,
	endAt time.Time,
	reason string,
	leaveType string,
	err error,
) {
	// 构建字段名到值的映射（忽略大小写）
	fieldMap := make(map[string]string, len(formValues))
	for _, fv := range formValues {
		name := strings.TrimSpace(fv.Name)
		value := strings.TrimSpace(fv.Value)
		if name == "" || value == "" {
			continue
		}

		// 处理钉钉 DDHolidayField 组件的特殊格式
		// name: "[\"开始时间\",\"结束时间\"]"
		// value: "[\"2026-01-06 13:51\",\"2026-01-06 13:52\",0.02,\"hour\",\"事假\",\"请假类型\"]"
		if strings.HasPrefix(name, "[") && strings.Contains(name, "开始时间") {
			var valueArr []interface{}
			if err := json.Unmarshal([]byte(value), &valueArr); err == nil && len(valueArr) >= 2 {
				// valueArr[0] = 开始时间, valueArr[1] = 结束时间
				if startStr, ok := valueArr[0].(string); ok {
					if t, e := parseTime(startStr); e == nil {
						startAt = t
					}
				}
				if endStr, ok := valueArr[1].(string); ok {
					if t, e := parseTime(endStr); e == nil {
						endAt = t
					}
				}
				// valueArr[4] = 请假类型（如 "事假"）
				if len(valueArr) >= 5 {
					if lt, ok := valueArr[4].(string); ok {
						leaveType = lt
					}
				}
			}
			continue
		}

		// 统一转小写作为 key，便于匹配
		key := strings.ToLower(name)
		fieldMap[key] = value
	}

	// 如果 DDHolidayField 已解析，跳过普通字段解析
	if startAt.IsZero() {
		// 解析开始时间
		startStr := firstMatch(fieldMap,
			"start_time", "starttime", "开始时间", "请假开始时间",
		)
		if startStr != "" {
			if t, e := parseTime(startStr); e == nil {
				startAt = t
			}
		}
	}

	if endAt.IsZero() {
		// 解析结束时间
		endStr := firstMatch(fieldMap,
			"end_time", "endtime", "结束时间", "请假结束时间",
		)
		if endStr != "" {
			if t, e := parseTime(endStr); e == nil {
				endAt = t
			}
		}
	}

	// 解析请假理由
	reason = firstMatch(fieldMap,
		"reason", "leave_reason", "leavereson", "请假理由", "事由", "请假事由",
	)

	// 解析请假类型（如果 DDHolidayField 未解析到）
	if leaveType == "" {
		leaveType = firstMatch(fieldMap,
			"leave_type", "leavetype", "请假类型", "类型",
		)
	}

	return startAt, endAt, reason, leaveType, nil
}

// firstMatch 在 fieldMap 中按优先级查找第一个匹配的字段值。
func firstMatch(fieldMap map[string]string, keys ...string) string {
	for _, key := range keys {
		if v, ok := fieldMap[strings.ToLower(key)]; ok && v != "" {
			return v
		}
	}
	return ""
}

// parseTime 解析时间字符串（支持多种格式）。
func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("时间字符串为空")
	}

	// 常见格式
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"2006/01/02",
		time.RFC3339,
		time.RFC3339Nano,
	}

	for _, fmt := range formats {
		if t, err := time.ParseInLocation(fmt, s, time.Local); err == nil {
			return t, nil
		}
	}

	// 尝试解析时间戳（毫秒）
	if ts, err := parseTimestamp(s); err == nil {
		return ts, nil
	}

	return time.Time{}, fmt.Errorf("无法解析时间格式: %s", s)
}

// parseTimestamp 尝试将字符串解析为时间戳（毫秒或秒）。
func parseTimestamp(s string) (time.Time, error) {
	// 移除可能的空格和引号
	s = strings.TrimSpace(strings.Trim(s, `"'`))

	var ts int64
	var err error
	// 尝试解析为毫秒（13位）或秒（10位）
	if len(s) == 13 {
		ts, err = parseInt64(s)
		if err == nil {
			return time.UnixMilli(ts), nil
		}
	} else if len(s) == 10 {
		ts, err = parseInt64(s)
		if err == nil {
			return time.Unix(ts, 0), nil
		}
	}

	return time.Time{}, errors.New("不是有效的时间戳")
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// normalizeStatus 归一化审批状态。
func normalizeStatus(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "UNKNOWN"
	}
	upper := strings.ToUpper(s)
	switch upper {
	case "RUNNING", "NEW", "ACTIVE":
		return "RUNNING"
	case "COMPLETED", "FINISHED", "DONE":
		return "COMPLETED"
	case "TERMINATED", "CANCELED", "CANCELLED":
		return "TERMINATED"
	default:
		return upper
	}
}

// normalizeResult 归一化审批结果。
func normalizeResult(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	switch lower {
	case "agree", "approved", "pass", "通过":
		return "agree"
	case "refuse", "rejected", "denied", "拒绝":
		return "refuse"
	case "cancel", "cancelled", "撤销":
		return "cancel"
	default:
		return lower
	}
}
