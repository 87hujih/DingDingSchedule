package inits

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"schedule_server/internal/model"

	"go.uber.org/zap/zapcore"
	"gorm.io/gorm"
)

// dbLogCore 是一个 Zap Core，将 Error+ 日志异步写入数据库。
// DB 通过 attachDB 延迟注入，避免日志与数据库的初始化顺序问题。
// DB 未注入前为 no-op（日志仍由文件 core 正常记录）。
type dbLogCore struct {
	db    atomic.Pointer[gorm.DB]
	level zapcore.Level
	with  []zapcore.Field // With() 预附加的字段
}

func newDBLogCore(level zapcore.Level) *dbLogCore {
	return &dbLogCore{level: level}
}

// attachDB 在 DB 初始化完成后调用（线程安全）。
func (c *dbLogCore) attachDB(db *gorm.DB) {
	c.db.Store(db)
}

func (c *dbLogCore) Enabled(level zapcore.Level) bool {
	return level >= c.level
}

func (c *dbLogCore) With(fields []zapcore.Field) zapcore.Core {
	clone := &dbLogCore{level: c.level}
	clone.db.Store(c.db.Load())
	clone.with = make([]zapcore.Field, len(c.with)+len(fields))
	copy(clone.with, c.with)
	copy(clone.with[len(c.with):], fields)
	return clone
}

func (c *dbLogCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) && c.db.Load() != nil {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *dbLogCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	db := c.db.Load()
	if db == nil {
		return nil
	}

	allFields := make([]zapcore.Field, 0, len(c.with)+len(fields))
	allFields = append(allFields, c.with...)
	allFields = append(allFields, fields...)

	caller := ""
	if entry.Caller.Defined {
		caller = entry.Caller.TrimmedPath()
	}

	record := &model.SystemLog{
		Level:   entry.Level.String(),
		Caller:  caller,
		Message: entry.Message,
		Fields:  serializeLogFields(allFields),
		Stack:   entry.Stack,
	}

	// 异步写入，避免阻塞日志调用方
	// SystemLog 无 TenantID 字段，租户隔离插件会自动跳过，无需 skip tenant ctx
	go func() {
		_ = db.WithContext(context.Background()).Create(record).Error
		// 写入失败故意忽略，避免触发新日志形成死循环
	}()

	return nil
}

func (c *dbLogCore) Sync() error { return nil }

// serializeLogFields 将 Zap 结构化字段序列化为 JSON 字符串。
func serializeLogFields(fields []zapcore.Field) string {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range fields {
		f.AddTo(enc)
	}
	b, err := json.Marshal(enc.Fields)
	if err != nil {
		return "{}"
	}
	return string(b)
}
