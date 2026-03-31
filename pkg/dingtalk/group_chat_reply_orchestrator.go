package dingtalk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

const (
	groupChatProcessTimeout  = 120 * time.Second
	groupChatAckDelay        = 4 * time.Second
	groupChatWebhookTimeout  = 5 * time.Second
	groupChatFallbackTimeout = 10 * time.Second
)

type groupChatReplyRequest struct {
	senderID       string
	senderNick     string
	sessionWebhook string
}

type groupChatReplyOrchestrator struct {
	logger            *zap.SugaredLogger
	sema              chan struct{}
	chatHandler       func(ctx context.Context, msg *ChatMessage) (string, error)
	asyncReplyHandler func(ctx context.Context, msg *ChatMessage, reply string)
	replyText         func(ctx context.Context, webhookURL, reply string) error
	processTimeout    time.Duration
	ackDelay          time.Duration
	webhookTimeout    time.Duration
	fallbackTimeout   time.Duration
}

// handle 负责接收群聊请求并在并发允许时启动异步处理流程。
func (o *groupChatReplyOrchestrator) handle(req groupChatReplyRequest, msg *ChatMessage) ([]byte, error) {
	if !o.tryAcquire() {
		o.sendBusyReply(req)
		return []byte(""), nil
	}

	go o.processAsync(req, msg)
	return []byte(""), nil
}

// tryAcquire 尝试获取并发处理信号量，超过上限时立即返回失败。
func (o *groupChatReplyOrchestrator) tryAcquire() bool {
	select {
	case o.sema <- struct{}{}:
		return true
	default:
		return false
	}
}

// processAsync 在后台完成群聊消息处理，并在需要时切换到主动推送回复。
func (o *groupChatReplyOrchestrator) processAsync(req groupChatReplyRequest, msg *ChatMessage) {
	// defer 逆序执行：recover 先于 sema 释放，确保 panic 时信号量也能释放
	defer func() { <-o.sema }()
	defer func() {
		if r := recover(); r != nil {
			o.logger.Errorw("群聊异步处理 panic", "senderID", req.senderID, "panic", r)
		}
	}()

	processCtx, processCancel := context.WithTimeout(context.Background(), o.processTimeout)
	defer processCancel()

	done := make(chan struct{})
	go o.sendSlowAck(req, done)

	reply, err := o.chatHandler(processCtx, msg)
	close(done)
	if err != nil {
		errMsg := "处理出错，请重试"
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "处理超时，请重试"
		}
		o.logger.Errorw("群聊处理失败", "senderID", msg.SenderID, "err", err)
		reply = fmt.Sprintf("@%s %s", msg.SenderNick, errMsg)
	}

	if err := o.sendWebhookReply(req, reply); err == nil {
		o.logger.Infow("通过 SessionWebhook 回复成功", "senderID", msg.SenderID)
		return
	} else {
		o.logger.Infow("SessionWebhook 已失效，切换主动推送", "senderID", msg.SenderID, "webhookErr", err)
	}

	if o.asyncReplyHandler == nil {
		o.logger.Errorw("主动推送处理器未注册，消息丢失", "senderID", msg.SenderID)
		return
	}

	fallbackCtx, fallbackCancel := context.WithTimeout(context.Background(), o.fallbackTimeout)
	defer fallbackCancel()
	o.asyncReplyHandler(fallbackCtx, msg, reply)
}

// sendBusyReply 在系统繁忙无法接单时通过会话 webhook 返回提示消息。
func (o *groupChatReplyOrchestrator) sendBusyReply(req groupChatReplyRequest) {
	ackCtx, ackCancel := context.WithTimeout(context.Background(), o.webhookTimeout)
	defer ackCancel()

	busyMsg := fmt.Sprintf("@%s 服务繁忙，请稍后重试", req.senderNick)
	if err := o.replyText(ackCtx, req.sessionWebhook, busyMsg); err != nil {
		o.logger.Errorw("发送繁忙提示失败", "senderID", req.senderID, "err", err)
	}
}

// sendSlowAck 在处理超过阈值时发送“处理中”提示，若已完成则不再发送。
func (o *groupChatReplyOrchestrator) sendSlowAck(req groupChatReplyRequest, done <-chan struct{}) {
	select {
	case <-time.After(o.ackDelay):
		ackCtx, ackCancel := context.WithTimeout(context.Background(), o.webhookTimeout)
		_ = o.replyText(ackCtx, req.sessionWebhook, fmt.Sprintf("@%s 正在查询，请稍候...", req.senderNick))
		ackCancel()
	case <-done:
	}
}

// sendWebhookReply 优先使用会话 webhook 回传最终回复内容。
func (o *groupChatReplyOrchestrator) sendWebhookReply(req groupChatReplyRequest, reply string) error {
	webhookCtx, webhookCancel := context.WithTimeout(context.Background(), o.webhookTimeout)
	defer webhookCancel()
	return o.replyText(webhookCtx, req.sessionWebhook, reply)
}
