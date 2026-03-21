package dto

import "errors"

// SignForUserRequest 代签请求
type SignForUserRequest struct {
	RecordID      uint   `json:"record_id"`       // 考勤记录ID
	Date          string `json:"date"`            // 考勤日期
	Section       int    `json:"section"`         // 考勤节次
	TargetUserIDs []uint `json:"target_user_ids"` // 目标用户ID列表
}

// Validate 校验代签请求参数
func (r SignForUserRequest) Validate() error {
	if len(r.TargetUserIDs) == 0 {
		return errors.New("target_user_ids is required")
	}
	if r.RecordID != 0 {
		return nil
	}
	if r.Date == "" || r.Section == 0 {
		return errors.New("record_id or date and section is required")
	}
	return nil
}

// SignForUserResponse 代签响应
type SignForUserResponse struct {
	SuccessIDs []uint `json:"success_ids"` // 成功代签的用户ID
	FailedIDs  []uint `json:"failed_ids"`  // 失败的用户ID（不在迟到列表中）
}
