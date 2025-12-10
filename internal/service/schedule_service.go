package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"

	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
	"schedule_server/pkg/scheduleparse"
	"schedule_server/pkg/weekutil"

	"gorm.io/gorm"
)

// ScheduleService 课表服务
type ScheduleService struct {
	courseRepo   repository.CourseRepository
	semesterRepo repository.SemesterRepository
}

// NewScheduleService 创建课表服务
func NewScheduleService(courseRepo repository.CourseRepository, semesterRepo repository.SemesterRepository) *ScheduleService {
	return &ScheduleService{
		courseRepo:   courseRepo,
		semesterRepo: semesterRepo,
	}
}

// ImportFromFile 导入课表（覆盖同一用户+学期数据），返回插入条数
func (s *ScheduleService) ImportFromFile(ctx context.Context, userID uint, semester, srcPath string) (int, error) {
	if userID == 0 {
		return 0, response.ErrForbidden()
	}
	if semester == "" {
		return 0, response.ErrInvalidParamWithMsg("semester 不能为空")
	}

	tmpXlsx, err := os.CreateTemp("", "schedule-*.xlsx")
	if err != nil {
		return 0, fmt.Errorf("create temp xlsx: %w", err)
	}
	tmpXlsxPath := tmpXlsx.Name()
	_ = tmpXlsx.Close()
	defer os.Remove(tmpXlsxPath)

	if err = scheduleparse.ConvertToXLSX(ctx, srcPath, tmpXlsxPath); err != nil {
		return 0, fmt.Errorf("convert to xlsx: %w", err)
	}

	parsedCourses, err := scheduleparse.ParseCourses(ctx, tmpXlsxPath)
	if err != nil {
		return 0, fmt.Errorf("parse courses: %w", err)
	}
	if len(parsedCourses) == 0 {
		return 0, response.ErrInvalidParamWithMsg("未解析到任何课程数据")
	}

	courses := make([]model.Course, 0, len(parsedCourses))
	for _, c := range parsedCourses {
		courses = append(courses, model.Course{
			UserID:     userID,
			Semester:   semester,
			CourseName: c.CourseName,
			Teacher:    c.Teacher,
			Location:   c.Location,
			DayOfWeek:  c.DayOfWeek,
			Section:    c.Section,
			WeekList:   c.WeekList,
		})
	}

	if err = s.courseRepo.ReplaceByUserSemester(ctx, userID, semester, courses); err != nil {
		return 0, fmt.Errorf("replace courses: %w", err)
	}

	return len(courses), nil
}

// WeekScheduleResult 按周查询结果
type WeekScheduleResult struct {
	CurrentWeek int
	TotalWeek   int
	Courses     []model.Course
}

// ListByWeek 查询指定周次的课表（不分页）
// week <= 0 则自动计算当前周
func (s *ScheduleService) ListByWeek(
	ctx context.Context,
	userID uint,
	semesterName string,
	week int,
) (*WeekScheduleResult, error) {
	if userID == 0 {
		return nil, response.ErrForbidden()
	}
	if semesterName == "" {
		return nil, response.ErrInvalidParamWithMsg("semester 不能为空")
	}

	// 1. 查询学期配置
	sem, err := s.semesterRepo.GetByName(ctx, semesterName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.ErrInvalidParamWithMsg("学期不存在，请先配置学期信息")
		}
		return nil, fmt.Errorf("get semester: %w", err)
	}

	// 2. 若未指定周次，计算当前周
	currentWeek := weekutil.CurrentWeek(sem.StartDate, sem.TotalWeek)
	if week <= 0 {
		week = currentWeek
	}

	// 3. 查询该用户该学期所有课程
	all, err := s.courseRepo.ListByUserSemester(ctx, userID, semesterName)
	if err != nil {
		return nil, fmt.Errorf("list courses: %w", err)
	}

	// 4. 过滤出该周有课的课程
	filtered := make([]model.Course, 0, len(all))
	for _, c := range all {
		if weekutil.ContainsWeek(c.WeekList, week) {
			filtered = append(filtered, c)
		}
	}

	return &WeekScheduleResult{
		CurrentWeek: currentWeek,
		TotalWeek:   sem.TotalWeek,
		Courses:     filtered,
	}, nil
}

// AllCoursesResult 全部课程查询结果
type AllCoursesResult struct {
	Total   int
	Courses []model.Course
}

// ListAll 查询用户某学期的全部课程（不按周过滤），支持分页
func (s *ScheduleService) ListAll(
	ctx context.Context,
	userID uint,
	semesterName string,
	page int,
	pageSize int,
) (*AllCoursesResult, error) {
	if userID == 0 {
		return nil, response.ErrForbidden()
	}
	if semesterName == "" {
		return nil, response.ErrInvalidParamWithMsg("semester 不能为空")
	}

	// 查询该用户该学期所有课程
	all, err := s.courseRepo.ListByUserSemester(ctx, userID, semesterName)
	if err != nil {
		return nil, fmt.Errorf("list courses: %w", err)
	}

	// 分页处理
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	total := len(all)
	start := (page - 1) * pageSize
	end := start + pageSize

	var courses []model.Course
	if start >= total {
		courses = []model.Course{}
	} else {
		if end > total {
			end = total
		}
		courses = all[start:end]
	}

	return &AllCoursesResult{
		Total:   total,
		Courses: courses,
	}, nil
}

// SaveUploadToTemp 保存上传文件到临时路径
func (s *ScheduleService) SaveUploadToTemp(
	ctx context.Context, filename string, reader func() (multipart.File, error)) (string, func(), error) {
	rc, err := reader()
	if err != nil {
		return "", nil, err
	}
	defer rc.Close()

	tmpFile, err := os.CreateTemp("", fmt.Sprintf("upload-*%s", filepath.Ext(filename)))
	if err != nil {
		return "", nil, err
	}

	if _, err = io.Copy(tmpFile, rc); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", nil, err
	}
	if err = tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", nil, err
	}

	cleanup := func() { _ = os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

// CreateCourse 手动添加单条课程，返回课程ID
func (s *ScheduleService) CreateCourse(ctx context.Context, userID uint, req *dto.CreateCourseRequest) (uint, error) {
	if userID == 0 {
		return 0, response.ErrForbidden()
	}

	course := &model.Course{
		UserID:     userID,
		Semester:   req.Semester,
		CourseName: req.CourseName,
		Teacher:    req.Teacher,
		Location:   req.Location,
		DayOfWeek:  req.DayOfWeek,
		Section:    req.Section,
		WeekList:   req.WeekList,
	}

	if err := s.courseRepo.Create(ctx, course); err != nil {
		return 0, err
	}
	return course.ID, nil
}

// UpdateCourse 更新课程，校验归属权限
func (s *ScheduleService) UpdateCourse(ctx context.Context, userID uint, courseID uint, req *dto.UpdateCourseRequest) error {
	if userID == 0 {
		return response.ErrForbidden()
	}

	// 1. 查询课程是否存在
	existing, err := s.courseRepo.GetByID(ctx, courseID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.ErrNotFoundWithMsg("课程不存在")
		}
		return fmt.Errorf("get course: %w", err)
	}

	// 2. 校验归属权限
	if existing.UserID != userID {
		return response.ErrForbidden()
	}

	// 3. 构造更新对象
	updates := &model.Course{
		ID:         courseID,
		CourseName: req.CourseName,
		Teacher:    req.Teacher,
		Location:   req.Location,
		DayOfWeek:  req.DayOfWeek,
		Section:    req.Section,
		WeekList:   req.WeekList,
	}
	return s.courseRepo.Update(ctx, updates)
}

// DeleteCourse 删除课程，校验归属权限
func (s *ScheduleService) DeleteCourse(ctx context.Context, userID uint, courseID uint) error {
	if userID == 0 {
		return response.ErrForbidden()
	}

	// 1. 查询课程是否存在
	existing, err := s.courseRepo.GetByID(ctx, courseID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.ErrNotFoundWithMsg("课程不存在")
		}
		return fmt.Errorf("get course: %w", err)
	}

	// 2. 校验归属权限
	if existing.UserID != userID {
		return response.ErrForbidden()
	}

	// 3. 执行删除
	return s.courseRepo.Delete(ctx, courseID)
}
