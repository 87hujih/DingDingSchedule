package errs

import "errors"

// MsgErr 用于保留底层错误链，但对外只暴露友好的消息文本。
//
// - Error() 仅返回 Msg（适合直接给前端/调用方提示）
// - Unwrap() 返回 Err，便于 errors.Is / errors.As 追溯真实原因
type MsgErr struct {
	Msg string
	Err error
}

func (e *MsgErr) Error() string { return e.Msg }
func (e *MsgErr) Unwrap() error { return e.Err }

// WrapMsgErr 包装错误：对外返回 msg，但保留原始 err 作为错误链。
// 若 err 为 nil，则返回 errors.New(msg)。
func WrapMsgErr(msg string, err error) error {
	if err == nil {
		return errors.New(msg)
	}
	return &MsgErr{Msg: msg, Err: err}
}
