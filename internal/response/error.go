package response

import (
	"errors"
	"fmt"
)

// BizError 业务错误，可在 service 层抛出，handler 层统一处理
type BizError struct {
	Code    int
	Message string
}

// Error 实现 error 接口
func (e *BizError) Error() string {
	return fmt.Sprintf("code: %d, message: %s", e.Code, e.Message)
}

// NewBizError 创建业务错误
func NewBizError(code int, message string) *BizError {
	return &BizError{
		Code:    code,
		Message: message,
	}
}

// NewBizErrorWithCode 根据错误码创建业务错误（自动填充消息）
func NewBizErrorWithCode(code int) *BizError {
	return &BizError{
		Code:    code,
		Message: GetMessage(code),
	}
}

// ===================== 常用业务错误快捷创建 =====================

// ErrForbidden 禁止访问
func ErrForbidden() *BizError {
	return NewBizErrorWithCode(CodeForbidden)
}

// ErrNotFound 资源不存在
func ErrNotFound() *BizError {
	return NewBizErrorWithCode(CodeNotFound)
}

// ErrInvalidParam 参数无效
func ErrInvalidParam() *BizError {
	return NewBizErrorWithCode(CodeInvalidParam)
}

// ErrInvalidParamWithMsg 参数无效（自定义消息）
func ErrInvalidParamWithMsg(msg string) *BizError {
	return NewBizError(CodeInvalidParam, msg)
}

// ErrUserNotFound 用户不存在
func ErrUserNotFound() *BizError {
	return NewBizErrorWithCode(CodeUserNotFound)
}

// ErrDingUserNotFound 钉钉用户不存在
func ErrDingUserNotFound() *BizError {
	return NewBizErrorWithCode(CodeDingUserNotFound)
}

// ErrDingAuthCodeInvalid 钉钉授权码无效
func ErrDingAuthCodeInvalid() *BizError {
	return NewBizErrorWithCode(CodeDingAuthCodeInvalid)
}

// ErrScheduleNotFound 排班不存在
func ErrScheduleNotFound() *BizError {
	return NewBizErrorWithCode(CodeScheduleNotFound)
}

// ErrAttendanceExpired 签到已过期
func ErrAttendanceExpired() *BizError {
	return NewBizErrorWithCode(CodeAttendanceExpired)
}

// ErrWiFiNotAllowed WiFi 不在允许范围
func ErrWiFiNotAllowed() *BizError {
	return NewBizErrorWithCode(CodeWiFiNotAllowed)
}

// ErrInternal 内部错误
func ErrInternal() *BizError {
	return NewBizErrorWithCode(CodeInternalError)
}

// IsBizError 判断是否为业务错误
func IsBizError(err error) bool {
	var bizError *BizError
	ok := errors.As(err, &bizError)
	return ok
}

// GetBizError 获取业务错误（如果是的话）
func GetBizError(err error) (*BizError, bool) {
	var bizErr *BizError
	ok := errors.As(err, &bizErr)
	return bizErr, ok
}
