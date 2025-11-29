package response

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ===================== 核心结构 =====================

// Response 统一响应结构
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Pagination 分页信息
type Pagination struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// PageResult 分页结果
type PageResult[T any] struct {
	List       []T        `json:"list"`
	Pagination Pagination `json:"pagination"`
}

// ===================== 抽象接口 =====================

// Transformer 数据转换接口，DTO 实现此接口可自动转换
type Transformer[From any, To any] interface {
	Transform(from From) To
}

// Responder Service 层返回结果接口
type Responder interface {
	IsSuccess() bool
	GetError() error
	GetData() interface{}
}

// ===================== Result 泛型包装器 =====================

// Result 泛型结果包装器，用于 Service 层统一返回
type Result[T any] struct {
	data T
	err  error
}

// NewResult 创建成功结果
func NewResult[T any](data T) *Result[T] {
	return &Result[T]{data: data}
}

// NewResultError 创建失败结果
func NewResultError[T any](err error) *Result[T] {
	return &Result[T]{err: err}
}

func (r *Result[T]) IsSuccess() bool      { return r.err == nil }
func (r *Result[T]) GetError() error      { return r.err }
func (r *Result[T]) GetData() interface{} { return r.data }
func (r *Result[T]) Unwrap() (T, error)   { return r.data, r.err }

// ===================== PagedResult 分页结果包装器 =====================

// PagedResult 分页结果包装器
type PagedResult[T any] struct {
	List     []T
	Total    int64
	Page     int
	PageSize int
	err      error
}

// NewPagedResult 创建分页结果
func NewPagedResult[T any](list []T, total int64, page, pageSize int) *PagedResult[T] {
	return &PagedResult[T]{List: list, Total: total, Page: page, PageSize: pageSize}
}

// NewPagedResultError 创建分页失败结果
func NewPagedResultError[T any](err error) *PagedResult[T] {
	return &PagedResult[T]{err: err}
}

func (r *PagedResult[T]) IsSuccess() bool { return r.err == nil }
func (r *PagedResult[T]) GetError() error { return r.err }
func (r *PagedResult[T]) GetData() interface{} {
	return PageResult[T]{
		List:       r.List,
		Pagination: Pagination{Page: r.Page, PageSize: r.PageSize, Total: r.Total},
	}
}

// ===================== Builder 响应构建器 =====================

// Builder 响应构建器，支持链式调用
type Builder struct {
	ctx        *gin.Context
	httpStatus int
	code       int
	message    string
	data       interface{}
}

// New 创建响应构建器
func New(ctx *gin.Context) *Builder {
	return &Builder{
		ctx:        ctx,
		httpStatus: http.StatusOK,
		code:       CodeSuccess,
		message:    "success",
	}
}

// Code 设置业务状态码
func (b *Builder) Code(code int) *Builder {
	b.code = code
	if b.message == "success" || b.message == "" {
		b.message = GetMessage(code)
	}
	return b
}

// Message 设置响应消息
func (b *Builder) Message(msg string) *Builder {
	b.message = msg
	return b
}

// Data 设置响应数据
func (b *Builder) Data(data interface{}) *Builder {
	b.data = data
	return b
}

// Result 从 Result 包装器设置数据，自动处理错误
func (b *Builder) Result(r Responder) *Builder {
	if !r.IsSuccess() {
		var bizErr *BizError
		if errors.As(r.GetError(), &bizErr) {
			b.code = bizErr.Code
			b.message = bizErr.Message
		}
		b.data = nil
	} else {
		b.data = r.GetData()
	}
	return b
}

// Transform 转换响应数据
func (b *Builder) Transform(fn func(interface{}) interface{}) *Builder {
	if b.data != nil {
		b.data = fn(b.data)
	}
	return b
}

// TransformList 泛型列表转换
func TransformList[From any, To any](list []From, fn func(From) To) []To {
	result := make([]To, 0, len(list))
	for _, item := range list {
		result = append(result, fn(item))
	}
	return result
}

// Page 设置分页数据
func (b *Builder) Page(list interface{}, total int64, page, pageSize int) *Builder {
	b.data = map[string]interface{}{
		"list": list,
		"pagination": Pagination{
			Page:     page,
			PageSize: pageSize,
			Total:    total,
		},
	}
	return b
}

// HTTPStatus 设置 HTTP 状态码（默认 200）
func (b *Builder) HTTPStatus(status int) *Builder {
	b.httpStatus = status
	return b
}

// Send 发送响应
func (b *Builder) Send() {
	b.ctx.JSON(b.httpStatus, Response{
		Code:    b.code,
		Message: b.message,
		Data:    b.data,
	})
}

// Abort 发送响应并中止后续处理（用于中间件）
func (b *Builder) Abort() {
	b.ctx.AbortWithStatusJSON(b.httpStatus, Response{
		Code:    b.code,
		Message: b.message,
		Data:    b.data,
	})
}

// ===================== 快捷方法 =====================

// OK 成功响应
func OK(ctx *gin.Context, data interface{}) {
	New(ctx).Data(data).Send()
}

// OKWithMessage 成功响应（自定义消息）
func OKWithMessage(ctx *gin.Context, message string, data interface{}) {
	New(ctx).Message(message).Data(data).Send()
}

// OKWithPage 分页成功响应
func OKWithPage(ctx *gin.Context, list interface{}, total int64, page, pageSize int) {
	New(ctx).Page(list, total, page, pageSize).Send()
}

// Fail 失败响应
func Fail(ctx *gin.Context, code int, message string) {
	New(ctx).Code(code).Message(message).Send()
}

// FailWithCode 根据错误码响应（自动填充消息）
func FailWithCode(ctx *gin.Context, code int) {
	New(ctx).Code(code).Send()
}

// FailWithError 根据 error 响应
func FailWithError(ctx *gin.Context, err error) {
	// 检查是否为自定义业务错误
	var bizErr *BizError
	if errors.As(err, &bizErr) {
		New(ctx).Code(bizErr.Code).Message(bizErr.Message).Send()
		return
	}
	New(ctx).Code(CodeInternalError).Message(err.Error()).Send()
}

// Forbidden 禁止访问响应
func Forbidden(ctx *gin.Context, message string) {
	New(ctx).HTTPStatus(http.StatusForbidden).Code(CodeForbidden).Message(message).Abort()
}

// DingAuthFail 钉钉认证失败响应
func DingAuthFail(ctx *gin.Context, message string) {
	New(ctx).Code(CodeDingAuthCodeInvalid).Message(message).Abort()
}
