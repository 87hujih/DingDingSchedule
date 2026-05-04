package agent

import (
	"context"

	"schedule_server/internal/agent/tools"

	"go.uber.org/zap"
)

const callLogChannelSize = 256

// callLogWriter 是一个有界的异步日志写入器，用带缓冲的 channel 替代 fire-and-forget goroutine。
type callLogWriter struct {
	ch     chan tools.CallLog
	port   tools.CallLogPort
	logger *zap.SugaredLogger
	done   chan struct{}
}

// newCallLogWriter 创建并启动日志写入 worker。
func newCallLogWriter(port tools.CallLogPort, logger *zap.SugaredLogger) *callLogWriter {
	w := &callLogWriter{
		ch:     make(chan tools.CallLog, callLogChannelSize),
		port:   port,
		logger: logger,
		done:   make(chan struct{}),
	}
	go w.run()
	return w
}

// Write 异步写入一条调用日志。channel 满时丢弃并记录警告。
func (w *callLogWriter) Write(log tools.CallLog) {
	if w == nil || w.port == nil {
		return
	}
	select {
	case w.ch <- log:
	default:
		if w.logger != nil {
			w.logger.Warnw("callLog channel 已满，丢弃日志", "user", log.UserName, "question", log.Question)
		}
	}
}

// Stop 停止 writer，排空 channel 中剩余的日志。
func (w *callLogWriter) Stop() {
	if w == nil {
		return
	}
	close(w.ch)
	<-w.done
}

// run 是后台 worker goroutine，从 channel 读取日志并写入。
func (w *callLogWriter) run() {
	defer close(w.done)
	for log := range w.ch {
		w.port.Write(context.Background(), log)
	}
}
