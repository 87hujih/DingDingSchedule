package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"schedule_server/internal/model"
	"schedule_server/internal/service"
	"schedule_server/internal/tenantctx"

	"github.com/gin-gonic/gin"
)

const maxBodySize = 4096 // 4KB

// AuditLog 操作审计日志中间件，只记录非 GET 请求
func AuditLog(auditSvc *service.AuditLogService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 只记录写操作
		if c.Request.Method == http.MethodGet {
			c.Next()
			return
		}

		start := time.Now()

		// 读取 body（限 4KB），然后恢复，让后续 handler 能正常读取
		// multipart/form-data（文件上传）不读取 body，避免截断导致解析失败
		var bodyStr string
		contentType := c.Request.Header.Get("Content-Type")
		isMultipart := len(contentType) >= 9 && contentType[:9] == "multipart"
		if c.Request.Body != nil && !isMultipart {
			bodyBytes, _ := io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize))
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			bodyStr = string(bodyBytes)
		}

		c.Next()

		// 提取用户信息（JWTAuth 中间件已在 context 中设置）
		userID, _ := c.Get(CtxKeyUserID)
		userName, _ := c.Get(CtxKeyUserName)
		userRole, _ := c.Get(CtxKeyUserRole)
		tenantID, _ := c.Get(CtxKeyTenantID)

		uid, _ := userID.(uint)
		name, _ := userName.(string)
		role, _ := userRole.(int)
		tid, _ := tenantID.(uint)

		entry := &model.AuditLog{
			TenantID:    tid,
			UserID:      uid,
			UserName:    name,
			UserRole:    role,
			Method:      c.Request.Method,
			Path:        c.FullPath(),
			StatusCode:  c.Writer.Status(),
			Duration:    time.Since(start).Milliseconds(),
			IPAddress:   c.ClientIP(),
			RequestBody: bodyStr,
		}

		// 异步写入，使用带 tenantID 的 background context，避免阻塞响应
		go func() {
			bgCtx := tenantctx.WithTenantID(context.Background(), tid)
			_ = auditSvc.Create(bgCtx, entry)
		}()
	}
}
