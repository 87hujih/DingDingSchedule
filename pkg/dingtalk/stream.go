package dingtalk

import (
	"context"
	"encoding/json"
	"strings"

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
			// 捕获主 goroutine 的 panic
			s.logger.Errorw("Stream 客户端 panic 已捕获", "panic", r)
		}
	}()

	cli := client.NewStreamClient(
		client.WithAppCredential(client.NewAppCredentialConfig(s.appKey, s.appSecret)),
		client.WithSubscription("EVENT", "*", s.handleEvent),
		client.WithAutoReconnect(true), // 使用 SDK 内置重连机制
	)

	s.client = cli

	// Start 是阻塞的，会一直运行直到 context 取消
	// 注意：SDK v0.9.1 在关闭时可能有 "send on closed channel" panic
	// 这是 SDK 内部 goroutine 的竞态问题，无法在此捕获
	if err := cli.Start(ctx); err != nil && err != context.Canceled {
		s.logger.Errorw("Stream 客户端启动失败", "err", err)
		return err
	}

	return ctx.Err()
}

// handleEvent 处理钉钉事件
func (s *StreamClient) handleEvent(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error) {
	// topic 是事件主题（如 bpms_instance_change），存在 df.Headers["topic"]，通过 df.GetTopic() 读取。
	// 注意：df.GetHeader("eventType") 读的是另一个字段，在直接订阅原始 DataFrame 时为空，不能用来判断事件类型。
	topic := df.GetTopic()
	eventCorpID := df.GetHeader("eventCorpId")

	// corpID 优先用事件 Header 里的，避免依赖 StreamClient 初始化时写死的值
	corpID := eventCorpID
	if corpID == "" {
		corpID = s.corpID
	}

	// 解析事件 Data 获取 processInstanceId 等字段
	var evt bpmsEvent
	if err := json.Unmarshal([]byte(df.Data), &evt); err != nil {
		s.logger.Warnw("解析事件数据失败", "data", df.Data, "err", err)
		return payload.NewSuccessDataFrameResponse(), nil
	}

	// 记录收到的事件（用于调试）
	s.logger.Infow("收到钉钉事件",
		"topic", topic,
		"eventCorpId", eventCorpID,
		"processInstanceId", evt.ProcessInstanceID,
		"status", evt.Status,
	)

	// 只处理审批实例变更事件（通过 topic 判断，不依赖 df.Data 里的 approveType）。
	// approveType 是各企业在审批表单中自定义的字段，不同企业的值不同，不可作为统一过滤条件。
	if s.onBpmsEvent == nil || evt.ProcessInstanceID == "" || !strings.Contains(topic, "bpms_instance") {
		s.logger.Debugw("跳过事件处理",
			"topic", topic,
			"processInstanceId", evt.ProcessInstanceID,
			"hasHandler", s.onBpmsEvent != nil,
		)
		return payload.NewSuccessDataFrameResponse(), nil
	}

	s.logger.Infow("开始处理审批实例变更事件",
		"topic", topic,
		"processInstanceId", evt.ProcessInstanceID,
		"status", evt.Status,
		"corpID", corpID,
	)

	go func() {
		if err := s.onBpmsEvent(context.Background(), corpID, evt.ProcessInstanceID, evt.Status); err != nil {
			s.logger.Errorw("处理审批事件失败",
				"processInstanceId", evt.ProcessInstanceID,
				"err", err,
			)
		} else {
			s.logger.Infow("处理审批事件成功",
				"processInstanceId", evt.ProcessInstanceID,
			)
		}
	}()

	return payload.NewSuccessDataFrameResponse(), nil
}

// bpmsEvent 审批事件结构（适配钉钉 Stream 推送格式）
type bpmsEvent struct {
	ProcessInstanceID string `json:"processInstanceId"` // 审批实例ID
	ProcessCode       string `json:"processCode"`       // 流程编码
	Status            string `json:"status"`            // start/finish/terminate
}
