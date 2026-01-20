package response

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
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

// ErrNotFoundWithMsg 资源不存在（自定义消息）
func ErrNotFoundWithMsg(msg string) *BizError {
	return NewBizError(CodeNotFound, msg)
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

// TranslateValidationError 将 validator 错误翻译为友好的中文提示
func TranslateValidationError(err error) string {
	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) {
		return err.Error()
	}

	var msgs []string
	for _, e := range validationErrs {
		field := translateFieldName(e.Field())
		msg := translateValidationTag(field, e.Tag(), e.Param())
		msgs = append(msgs, msg)
	}

	return strings.Join(msgs, "; ")
}

// translateFieldName 字段名翻译
func translateFieldName(field string) string {
	fieldMap := map[string]string{
		"Name":       "名称",
		"StartDate":  "开始日期",
		"TotalWeek":  "总周数",
		"AuthCode":   "授权码",
		"CorpID":     "企业ID",
		"CourseName": "课程名称",
		"DayOfWeek":  "星期",
		"Section":    "节次",
		"WeekList":   "周次列表",
		"Teacher":    "教师",
		"Location":   "地点",
	}
	if name, ok := fieldMap[field]; ok {
		return name
	}
	return field
}

// translateValidationTag 验证规则翻译
func translateValidationTag(field, tag, param string) string {
	switch tag {
	case "required":
		return fmt.Sprintf("%s不能为空", field)
	case "min":
		return fmt.Sprintf("%s不能小于%s", field, param)
	case "max":
		return fmt.Sprintf("%s不能大于%s", field, param)
	case "len":
		return fmt.Sprintf("%s长度必须为%s", field, param)
	case "email":
		return fmt.Sprintf("%s格式不正确", field)
	case "oneof":
		return fmt.Sprintf("%s必须是以下值之一: %s", field, param)
	default:
		return fmt.Sprintf("%s验证失败(%s)", field, tag)
	}
}
