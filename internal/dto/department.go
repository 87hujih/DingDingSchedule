package dto

import "schedule_server/internal/model"

// DepartmentItem 部门信息项
type DepartmentItem struct {
	DeptID   int64  `json:"dept_id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parent_id"`
}

// DepartmentListResponse 部门列表响应
type DepartmentListResponse struct {
	Items []DepartmentItem `json:"items"`
}

// NewDepartmentListResponse 从 model.Department 切片构造响应
func NewDepartmentListResponse(depts []model.Department) *DepartmentListResponse {
	items := make([]DepartmentItem, 0, len(depts))
	for _, d := range depts {
		items = append(items, DepartmentItem{
			DeptID:   d.DeptID,
			Name:     d.Name,
			ParentID: d.ParentID,
		})
	}
	return &DepartmentListResponse{Items: items}
}

// UpdateDeptStatusRequest 更新部门考勤状态请求
type UpdateDeptStatusRequest struct {
	Status int `json:"status" binding:"oneof=0 1"` // 部门状态(0:不参与考勤;1:参与考勤)
}
