package service

import (
	"context"
	"errors"
	"time"

	"schedule_server/internal/model"
	"schedule_server/internal/repository"

	"gorm.io/gorm"
)

// SemesterService 学期服务
type SemesterService struct {
	repo repository.SemesterRepository
}

// NewSemesterService 创建学期服务实例
func NewSemesterService(repo repository.SemesterRepository) *SemesterService {
	return &SemesterService{repo: repo}
}

// GetActiveSemester 获取当前租户的激活学期
func (s *SemesterService) GetActiveSemester(ctx context.Context) (*model.Semester, error) {
	return s.repo.GetActiveSemester(ctx)
}

// CalculateWeekFromDate 根据日期计算周数
// 返回值：week（1-based），错误信息
func (s *SemesterService) CalculateWeekFromDate(semester *model.Semester, date time.Time) (int, error) {
	if semester == nil {
		return 0, errors.New("学期未配置")
	}

	// 只比较日期部分
	startDate := time.Date(semester.StartDate.Year(), semester.StartDate.Month(), semester.StartDate.Day(), 0, 0, 0, 0, time.Local)
	checkDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.Local)

	days := int(checkDate.Sub(startDate).Hours() / 24)
	if days < 0 {
		return 0, errors.New("日期在学期开始之前")
	}

	week := days/7 + 1
	if week > semester.TotalWeeks {
		return week, errors.New("日期超出学期范围")
	}

	return week, nil
}

// ValidateWeekDate 校验周数与日期是否一致
// 返回 nil 表示校验通过，返回 error 表示不一致
func (s *SemesterService) ValidateWeekDate(ctx context.Context, date time.Time, week int) error {
	semester, err := s.repo.GetActiveSemester(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 学期未配置，跳过校验
			return nil
		}
		return err
	}

	derivedWeek, err := s.CalculateWeekFromDate(semester, date)
	if err != nil {
		// 日期不在学期范围内，跳过校验（或根据配置决定是否报错）
		return nil
	}

	if derivedWeek != week {
		return errors.New("周数与日期不一致")
	}

	return nil
}

// IsMonday 检查日期是否为周一
func (s *SemesterService) IsMonday(date time.Time) bool {
	return date.Weekday() == time.Monday
}

// GetCurrentWeek 获取当前周数
func (s *SemesterService) GetCurrentWeek(ctx context.Context) (int, error) {
	semester, err := s.repo.GetActiveSemester(ctx)
	if err != nil {
		return 0, err
	}
	return s.CalculateWeekFromDate(semester, time.Now())
}
