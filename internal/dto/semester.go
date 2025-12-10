package dto

import (
	"schedule_server/internal/model"
	"time"
)

// CreateSemesterRequest 创建学期请求
type CreateSemesterRequest struct {
	Name      string `json:"name" binding:"required"`       // 学期标识，如 "2025-Spring"
	StartDate string `json:"start_date" binding:"required"` // 学期第一周周一，格式 "2025-02-24"
	TotalWeek int    `json:"total_week" binding:"required,min=1,max=30"`
}

// UpdateSemesterRequest 更新学期请求
type UpdateSemesterRequest struct {
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	TotalWeek int    `json:"total_week" binding:"omitempty,min=1,max=30"`
}

// SemesterItem 学期信息项
type SemesterItem struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	StartDate time.Time `json:"start_date"`
	TotalWeek int       `json:"total_week"`
}

// SemesterListResponse 学期列表响应
type SemesterListResponse struct {
	Items []SemesterItem `json:"items"`
}

// NewSemesterListResponse 从 model.Semester 切片构造响应
func NewSemesterListResponse(semesters []model.Semester) *SemesterListResponse {
	items := make([]SemesterItem, 0, len(semesters))
	for _, s := range semesters {
		items = append(items, SemesterItem{
			ID:        s.ID,
			Name:      s.Name,
			StartDate: s.StartDate,
			TotalWeek: s.TotalWeek,
		})
	}
	return &SemesterListResponse{Items: items}
}

// CreateSemesterResponse 创建学期响应
type CreateSemesterResponse struct {
	ID uint `json:"id"`
}
