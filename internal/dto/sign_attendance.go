package dto

// SignForUserRequest 代签请求
type SignForUserRequest struct {
	TargetUserIDs []uint `json:"target_user_ids" binding:"required,min=1"` // 目标用户ID列表
}

// SignForUserResponse 代签响应
type SignForUserResponse struct {
	SuccessIDs []uint `json:"success_ids"` // 成功代签的用户ID
	FailedIDs  []uint `json:"failed_ids"`  // 失败的用户ID（不在迟到列表中）
}
