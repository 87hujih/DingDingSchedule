package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/payload"
	"go.uber.org/zap"
)

// groupChatConcurrency 群聊并发处理上限，超出后立即告知用户服务繁忙
const groupChatConcurrency = 10

// StreamClient 钉钉 Stream 模式客户端
type StreamClient struct {
	appKey    string
	appSecret string
	corpID    string
	logger    *zap.SugaredLogger
	client    *client.StreamClient

	// 事件处理回调
	onBpmsEvent func(ctx context.Context, corpID, processInstanceID, eventType string) error
	// 聊天消息处理回调
	chatHandler func(ctx context.Context, msg *ChatMessage) (string, error)

	// SessionWebhook 失效后的主动推送兜底（群聊专用）
	asyncReplyHandler func(ctx context.Context, msg *ChatMessage, reply string)
	// 并发信号量
	sema chan struct{}
}

// NewStreamClient 创建 Stream 客户端
func NewStreamClient(appKey, appSecret, corpID string, logger *zap.SugaredLogger) *StreamClient {
	return &StreamClient{
		appKey:    appKey,
		appSecret: appSecret,
		corpID:    corpID,
		logger:    logger,
		sema:      make(chan struct{}, groupChatConcurrency),
	}
}

// SetBpmsEventHandler 设置审批事件处理器
func (s *StreamClient) SetBpmsEventHandler(handler func(ctx context.Context, corpID, processInstanceID, eventType string) error) {
	s.onBpmsEvent = handler
}

// ChatMessage 机器人收到的聊天消息（解析后的结构）
type ChatMessage struct {
	CorpID            string
	SenderID          string
	SenderNick        string
	Content           string
	ConversationID    string
	ConversationType  string // "1"=单聊, "2"=群聊
	ConversationTitle string
	SessionWebhook    string
}

// SetChatMessageHandler 设置聊天消息处理器
func (s *StreamClient) SetChatMessageHandler(handler func(ctx context.Context, msg *ChatMessage) (string, error)) {
	s.chatHandler = handler
}

// SetAsyncReplyHandler 设置 SessionWebhook 失效时的兜底推送处理器（群聊专用）
func (s *StreamClient) SetAsyncReplyHandler(handler func(ctx context.Context, msg *ChatMessage, reply string)) {
	s.asyncReplyHandler = handler
}

var mentionRegexp = regexp.MustCompile(`@\S+\s*`)

// Start 启动 Stream 客户端（阻塞，使用 SDK 内置重连）
func (s *StreamClient) Start(ctx context.Context) error {
	defer func() {
		if r := recover(); r != nil {
			// 捕获主 goroutine 的 panic
			s.logger.Errorw("Stream 客户端 panic 已捕获", "panic", r)
		}
	}()

	opts := []client.ClientOption{
		client.WithAppCredential(client.NewAppCredentialConfig(s.appKey, s.appSecret)),
		client.WithSubscription("EVENT", "*", s.handleEvent),
		client.WithAutoReconnect(true),
	}

	// 注册机器人消息回调
	if s.chatHandler != nil {
		chatbotHandler := chatbot.NewDefaultChatBotFrameHandler(s.handleChatBotMessage)
		opts = append(opts, client.WithSubscription("CALLBACK", "/v1.0/im/bot/messages/get", chatbotHandler.OnEventReceived))
	}

	cli := client.NewStreamClient(opts...)

	s.client = cli

	// Start 是阻塞的，会一直运行直到 context 取消
	if err := cli.Start(ctx); err != nil && err != context.Canceled {
		s.logger.Errorw("Stream 客户端启动失败", "err", err)
		return err
	}

	return ctx.Err()
}

// handleChatBotMessage 处理机器人聊天消息（SDK chatbot 框架回调）
func (s *StreamClient) handleChatBotMessage(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	s.logger.Infow("收到钉钉消息回调",
		"senderID", data.SenderStaffId,
		"senderNick", data.SenderNick,
		"convType", data.ConversationType,
		"content", data.Text.Content,
	)

	if s.chatHandler == nil {
		s.logger.Warnw("chatHandler 未注册，消息被丢弃", "senderID", data.SenderStaffId)
		return []byte(""), nil
	}

	content := strings.TrimSpace(data.Text.Content)
	// 群聊时剥离 @mention
	if data.ConversationType == "2" {
		content = mentionRegexp.ReplaceAllString(content, "")
		content = strings.TrimSpace(content)
	}

	msg := &ChatMessage{
		CorpID:            data.SenderCorpId,
		SenderID:          data.SenderStaffId,
		SenderNick:        data.SenderNick,
		Content:           content,
		ConversationID:    data.ConversationId,
		ConversationType:  data.ConversationType,
		ConversationTitle: data.ConversationTitle,
		SessionWebhook:    data.SessionWebhook,
	}

	// 群聊：走异步流程（SessionWebhook 优先，过期则主动推送兜底）
	if data.ConversationType == "2" {
		return s.handleGroupChatAsync(data, msg)
	}

	// 私聊：保持同步，55s 超时留余量在 Webhook 60s 内完成
	processCtx, processCancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer processCancel()

	reply, err := s.chatHandler(processCtx, msg)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			reply = "处理超时，请重试"
		} else {
			s.logger.Errorw("处理私聊消息失败", "senderID", data.SenderStaffId, "err", err)
			reply = "处理出错，请重试"
		}
	}

	replyCtx, replyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer replyCancel()
	replier := chatbot.NewChatbotReplier()
	if err := replier.SimpleReplyText(replyCtx, data.SessionWebhook, []byte(renderPlainTextReply(reply))); err != nil {
		s.logger.Errorw("回复私聊消息失败", "senderID", data.SenderStaffId, "err", err)
	}

	return []byte(""), nil
}

// handleGroupChatAsync 群聊异步处理：立即返回，goroutine 完成后智能选择回复通道。
// 优先使用 SessionWebhook（快速响应时仍有效），失效后自动切换主动推送接口。
func (s *StreamClient) handleGroupChatAsync(data *chatbot.BotCallbackDataModel, msg *ChatMessage) ([]byte, error) {
	orch := s.newGroupChatReplyOrchestrator()
	return orch.handle(groupChatReplyRequest{
		senderID:       data.SenderStaffId,
		senderNick:     data.SenderNick,
		sessionWebhook: data.SessionWebhook,
	}, msg)
}

func (s *StreamClient) newGroupChatReplyOrchestrator() *groupChatReplyOrchestrator {
	return &groupChatReplyOrchestrator{
		logger:            s.logger,
		sema:              s.sema,
		chatHandler:       s.chatHandler,
		asyncReplyHandler: s.asyncReplyHandler,
		replyText: func(ctx context.Context, webhookURL, reply string) error {
			replier := chatbot.NewChatbotReplier()
			return replier.SimpleReplyText(ctx, webhookURL, []byte(renderPlainTextReply(reply)))
		},
		processTimeout:  groupChatProcessTimeout,
		ackDelay:        groupChatAckDelay,
		webhookTimeout:  groupChatWebhookTimeout,
		fallbackTimeout: groupChatFallbackTimeout,
	}
}

// handleEvent 处理钉钉事件
func (s *StreamClient) handleEvent(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error) {
	// 从 Header 获取企业ID，fallback 到 StreamClient 初始化时的值。
	// 注意：订阅 "*" 时 df.GetTopic() 返回的是 "*"（精确匹配订阅 key），不是实际事件名，不可用于判断事件类型。
	eventCorpID := df.GetHeader("eventCorpId")
	corpID := eventCorpID
	if corpID == "" {
		corpID = s.corpID
	}

	// 解析事件 Data
	var evt bpmsEvent
	if err := json.Unmarshal([]byte(df.Data), &evt); err != nil {
		s.logger.Warnw("解析事件数据失败", "data", df.Data, "err", err)
		return payload.NewSuccessDataFrameResponse(), nil
	}

	// 通过 processInstanceId 是否存在来判断是否为审批事件。
	// 不依赖 df.Headers（topic 为 "*"，eventType 可能为空）和 df.Data 里的 approveType（各企业自定义字段，值不统一）。
	// 非请假类审批（报销等）会在 LeaveSyncService 中通过表单字段解析结果过滤。
	if s.onBpmsEvent == nil || evt.ProcessInstanceID == "" {
		return payload.NewSuccessDataFrameResponse(), nil
	}

	s.logger.Infow("收到审批事件",
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
