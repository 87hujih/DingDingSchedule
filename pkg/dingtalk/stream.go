package dingtalk

import (
	"context"
	"encoding/json"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/payload"
	"go.uber.org/zap"
)

// StreamClient 钉钉 Stream 模式客户端
type StreamClient struct {
	appKey    string
	appSecret string
	corpID    string
	logger    *zap.SugaredLogger
	client    *client.StreamClient

	// 事件处理回调
	onBpmsEvent func(ctx context.Context, corpID, processInstanceID, eventType string) error
}

// NewStreamClient 创建 Stream 客户端
func NewStreamClient(appKey, appSecret, corpID string, logger *zap.SugaredLogger) *StreamClient {
	return &StreamClient{
		appKey:    appKey,
		appSecret: appSecret,
		corpID:    corpID,
		logger:    logger,
	}
}

// SetBpmsEventHandler 设置审批事件处理器
func (s *StreamClient) SetBpmsEventHandler(handler func(ctx context.Context, corpID, processInstanceID, eventType string) error) {
	s.onBpmsEvent = handler
}

// Start 启动 Stream 客户端（阻塞，使用 SDK 内置重连）
func (s *StreamClient) Start(ctx context.Context) error {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Errorw("Stream 客户端 panic 已捕获", "panic", r)
		}
	}()

	cli := client.NewStreamClient(
		client.WithAppCredential(client.NewAppCredentialConfig(s.appKey, s.appSecret)),
		client.WithSubscription("EVENT", "*", s.handleEvent),
		client.WithAutoReconnect(true), // 使用 SDK 内置重连机制
	)

	s.client = cli
	s.logger.Infow("钉钉 Stream 客户端启动中...", "appKey", s.appKey)

	// Start 是阻塞的，会一直运行直到 context 取消
	if err := cli.Start(ctx); err != nil && err != context.Canceled {
		s.logger.Errorw("Stream 客户端启动失败", "err", err)
		return err
	}

	s.logger.Infow("钉钉 Stream 客户端已停止")
	return ctx.Err()
}

// handleEvent 处理钉钉事件
func (s *StreamClient) handleEvent(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error) {
	// 打印原始数据用于调试
	s.logger.Infow("收到钉钉原始事件", "data", df.Data)

	// 解析事件数据
	var evt bpmsEvent
	if err := json.Unmarshal([]byte(df.Data), &evt); err != nil {
		s.logger.Warnw("解析事件数据失败", "data", df.Data, "err", err)
		return payload.NewSuccessDataFrameResponse(), nil
	}

	s.logger.Infow("收到钉钉事件",
		"approveType", evt.ApproveType,
		"status", evt.Status,
		"processInstanceId", evt.ProcessInstanceID,
	)

	// 处理请假审批事件
	if s.onBpmsEvent != nil && evt.ProcessInstanceID != "" && evt.ApproveType == "LEAVE" {
		go func() {
			if err := s.onBpmsEvent(context.Background(), s.corpID, evt.ProcessInstanceID, evt.Status); err != nil {
				s.logger.Errorw("处理审批事件失败",
					"processInstanceId", evt.ProcessInstanceID,
					"err", err,
				)
			}
		}()
	}

	return payload.NewSuccessDataFrameResponse(), nil
}

// bpmsEvent 审批事件结构（适配钉钉 Stream 推送格式）
type bpmsEvent struct {
	ApproveType       string `json:"approveType"`       // LEAVE=请假
	ProcessInstanceID string `json:"processInstanceId"` // 审批实例ID
	ProcessCode       string `json:"processCode"`       // 流程编码
	Status            string `json:"status"`            // start/finish/terminate
	EventID           string `json:"eventId"`           // 事件ID
}
