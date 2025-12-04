package response

// ===================== 错误码规范 =====================
// 0        : 成功
// 10xxx    : 钉钉/用户身份相关
// 20xxx    : 参数/请求相关
// 30xxx    : 业务逻辑相关
// 40xxx    : 系统/服务相关
// =====================================================

const (
	CodeSuccess = 0 // 成功

	// 钉钉身份 10xxx
	CodeUnauthorized        = 10000 // 未登录/Token无效
	CodeDingUserNotFound    = 10001 // 钉钉用户不存在
	CodeDingUserUnbind      = 10002 // 用户未绑定
	CodeDingAuthCodeInvalid = 10003 // 钉钉授权码无效
	CodeForbidden           = 10004 // 无权限访问

	// 参数请求 20xxx
	CodeInvalidParam       = 20001 // 参数无效
	CodeMissingParam       = 20002 // 缺少必要参数
	CodeParamTypeMismatch  = 20003 // 参数类型错误
	CodeRequestTooFrequent = 20004 // 请求过于频繁

	// 业务逻辑 30xxx
	CodeNotFound        = 30001 // 资源不存在
	CodeAlreadyExists   = 30002 // 资源已存在
	CodeOperationFailed = 30003 // 操作失败
	CodeStatusInvalid   = 30004 // 状态不允许此操作

	// 用户相关 31xxx
	CodeUserNotFound = 31001 // 用户不存在
	CodeUserExists   = 31002 // 用户已存在
	CodeUserDisabled = 31003 // 用户已禁用

	// 考勤/排班相关 32xxx
	CodeScheduleNotFound   = 32001 // 排班不存在
	CodeScheduleConflict   = 32002 // 排班冲突
	CodeAttendanceExists   = 32003 // 已签到/签退
	CodeAttendanceNotStart = 32004 // 未到签到时间
	CodeAttendanceExpired  = 32005 // 签到已过期
	CodeWiFiNotAllowed     = 32006 // 不在允许的 WiFi 范围

	// 系统服务 40xxx
	CodeInternalError = 40001 // 服务器内部错误
	CodeDBError       = 40002 // 数据库错误
	CodeCacheError    = 40003 // 缓存错误
	CodeServiceBusy   = 40004 // 服务繁忙

	// 钉钉服务 50xxx
	CodeDingTalkError     = 50001 // 钉钉服务异常
	CodeDingTokenError    = 50002 // 获取钉钉Token失败
	CodeDingUserInfoError = 50003 // 获取钉钉用户信息失败
	CodeDingDeptError     = 50004 // 获取钉钉部门信息失败
	CodeDingMessageError  = 50005 // 发送钉钉消息失败
)

// codeMessages 错误码消息映射
var codeMessages = map[int]string{
	CodeSuccess: "success",

	// 钉钉身份
	CodeUnauthorized:        "未登录或Token无效",
	CodeDingUserNotFound:    "钉钉用户不存在",
	CodeDingUserUnbind:      "用户未绑定",
	CodeDingAuthCodeInvalid: "钉钉授权码无效",
	CodeForbidden:           "无权限访问",

	// 参数请求
	CodeInvalidParam:       "参数无效",
	CodeMissingParam:       "缺少必要参数",
	CodeParamTypeMismatch:  "参数类型错误",
	CodeRequestTooFrequent: "请求过于频繁，请稍后再试",

	// 业务逻辑
	CodeNotFound:        "资源不存在",
	CodeAlreadyExists:   "资源已存在",
	CodeOperationFailed: "操作失败",
	CodeStatusInvalid:   "当前状态不允许此操作",

	// 用户相关
	CodeUserNotFound: "用户不存在",
	CodeUserExists:   "用户已存在",
	CodeUserDisabled: "用户已禁用",

	// 考勤/排班相关
	CodeScheduleNotFound:   "排班不存在",
	CodeScheduleConflict:   "排班冲突",
	CodeAttendanceExists:   "已签到/签退",
	CodeAttendanceNotStart: "未到签到时间",
	CodeAttendanceExpired:  "签到已过期",
	CodeWiFiNotAllowed:     "不在允许的 WiFi 范围",

	// 系统服务
	CodeInternalError: "服务器内部错误",
	CodeDBError:       "数据库异常",
	CodeCacheError:    "缓存异常",
	CodeServiceBusy:   "服务繁忙，请稍后再试",

	// 钉钉服务
	CodeDingTalkError:     "钉钉服务异常",
	CodeDingTokenError:    "获取钉钉Token失败",
	CodeDingUserInfoError: "获取钉钉用户信息失败",
	CodeDingDeptError:     "获取钉钉部门信息失败",
	CodeDingMessageError:  "发送钉钉消息失败",
}

// GetMessage 获取错误码对应的消息
func GetMessage(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "未知错误"
}

// RegisterCode 注册自定义错误码（用于业务扩展）
func RegisterCode(code int, message string) {
	codeMessages[code] = message
}
