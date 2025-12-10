package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"

	"gorm.io/gorm"
)

// SemesterService 学期服务
type SemesterService struct {
	semesterRepo repository.SemesterRepository
}

// NewSemesterService 创建学期服务
func NewSemesterService(semesterRepo repository.SemesterRepository) *SemesterService {
	return &SemesterService{semesterRepo: semesterRepo}
}

// List 查询所有学期
func (s *SemesterService) List(ctx context.Context) ([]model.Semester, error) {
	return s.semesterRepo.List(ctx)
}

// Create 创建学期
func (s *SemesterService) Create(ctx context.Context, name string, startDate time.Time, totalWeek int) (*model.Semester, error) {
	if name == "" {
		return nil, response.ErrInvalidParamWithMsg("name 不能为空")
	}

	// 检查是否已存在同名学期
	existing, err := s.semesterRepo.GetByName(ctx, name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check semester: %w", err)
	}
	if existing != nil {
		return nil, response.ErrInvalidParamWithMsg("学期名称已存在")
	}

	semester := &model.Semester{
		Name:      name,
		StartDate: startDate,
		TotalWeek: totalWeek,
	}
	if err := s.semesterRepo.Create(ctx, semester); err != nil {
		return nil, fmt.Errorf("create semester: %w", err)
	}
	return semester, nil
}

// Update 更新学期
func (s *SemesterService) Update(ctx context.Context, id uint, name string, startDate *time.Time, totalWeek int) error {
	// 1. 查询学期是否存在
	existing, err := s.semesterRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.ErrNotFoundWithMsg("学期不存在")
		}
		return fmt.Errorf("get semester: %w", err)
	}

	// 2. 如果更新名称，检查是否与其他学期重名
	if name != "" && name != existing.Name {
		other, err := s.semesterRepo.GetByName(ctx, name)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check semester name: %w", err)
		}
		if other != nil && other.ID != id {
			return response.ErrInvalidParamWithMsg("学期名称已存在")
		}
		existing.Name = name
	}

	// 3. 更新其他字段
	if startDate != nil {
		existing.StartDate = *startDate
	}
	if totalWeek > 0 {
		existing.TotalWeek = totalWeek
	}

	return s.semesterRepo.Update(ctx, existing)
}

// Delete 删除学期
func (s *SemesterService) Delete(ctx context.Context, id uint) error {
	// 1. 查询学期是否存在
	_, err := s.semesterRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.ErrNotFoundWithMsg("学期不存在")
		}
		return fmt.Errorf("get semester: %w", err)
	}

	return s.semesterRepo.Delete(ctx, id)
}
