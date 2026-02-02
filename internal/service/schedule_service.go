package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"

	"schedule_server/internal/consts"
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
	userRepo     repository.UserRepository
	courseRepo   repository.CourseRepository
	semesterRepo repository.SemesterRepository
}

// NewScheduleService 创建课表服务
func NewScheduleService(
	courseRepo repository.CourseRepository,
	userRepo repository.UserRepository,
	semesterRepo repository.SemesterRepository,
) *ScheduleService {
	return &ScheduleService{
		userRepo:     userRepo,
		courseRepo:   courseRepo,
		semesterRepo: semesterRepo,
	}
}

// ImportFromFile 导入课表（覆盖同一用户全部数据），返回插入条数
func (s *ScheduleService) ImportFromFile(ctx context.Context, userID uint, srcPath string) (int, error) {
	if userID == 0 {
		return 0, response.ErrForbidden()
	}

	// 获取当前激活学期ID
	var semesterID *uint
	if semester, err := s.semesterRepo.GetActiveSemester(ctx); err == nil && semester != nil {
		semesterID = &semester.ID
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
			SemesterID: semesterID,
			CourseName: c.CourseName,
			Teacher:    c.Teacher,
			Location:   c.Location,
			DayOfWeek:  c.DayOfWeek,
			Section:    c.Section,
			WeekList:   c.WeekList,
		})
	}

	if err = s.courseRepo.ReplaceByUser(ctx, userID, courses); err != nil {
		return 0, fmt.Errorf("replace courses: %w", err)
	}

	return len(courses), nil
}

// WeekScheduleResult 按周查询结果
type WeekScheduleResult struct {
	Courses []model.Course
}

// ListByWeek 查询指定周次的课表（不分页）
func (s *ScheduleService) ListByWeek(
	ctx context.Context,
	viewerID uint,
	viewerRole int,
	targetUserID uint,
	week int,
) (*WeekScheduleResult, error) {
	if week <= 0 {
		return nil, response.ErrInvalidParamWithMsg("week 不能为空")
	}

	// 0. 权限校验并确定实际查询的用户
	finalUserID, err := s.resolveTargetUserID(ctx, viewerID, viewerRole, targetUserID)
	if err != nil {
		return nil, err
	}

	// 1. 查询该用户所有课程
	all, err := s.courseRepo.ListByUser(ctx, finalUserID)
	if err != nil {
		return nil, fmt.Errorf("list courses: %w", err)
	}

	// 2. 过滤出该周有课的课程
	filtered := make([]model.Course, 0, len(all))
	for _, c := range all {
		if weekutil.ContainsWeek(c.WeekList, week) {
			filtered = append(filtered, c)
		}
	}

	return &WeekScheduleResult{
		Courses: filtered,
	}, nil
}

// AllCoursesResult 全部课程查询结果
type AllCoursesResult struct {
	Page     int
	PageSize int
	Total    int
	Courses  []model.Course
}

func normalizePagination(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}

// ListAll 查询用户的全部课程（不按周过滤），支持分页
func (s *ScheduleService) ListAll(
	ctx context.Context,
	userID uint,
	page int,
	pageSize int,
) (*AllCoursesResult, error) {
	if userID == 0 {
		return nil, response.ErrForbidden()
	}
	page, pageSize = normalizePagination(page, pageSize)

	courses, total, err := s.courseRepo.ListByUserPaged(ctx, userID, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list courses: %w", err)
	}

	return &AllCoursesResult{
		Page:     page,
		PageSize: pageSize,
		Total:    int(total),
		Courses:  courses,
	}, nil
}

// SaveUploadToTemp 保存上传文件到临时路径
func (s *ScheduleService) SaveUploadToTemp(
	ctx context.Context,
	filename string,
	reader func() (multipart.File, error),
) (string, func(), error) {
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

	// 获取当前激活学期ID
	var semesterID *uint
	if semester, err := s.semesterRepo.GetActiveSemester(ctx); err == nil && semester != nil {
		semesterID = &semester.ID
	}

	course := &model.Course{
		UserID:     userID,
		SemesterID: semesterID,
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

// GetCourseDetail 获取课程详情，校验归属权限
func (s *ScheduleService) GetCourseDetail(ctx context.Context, userID uint, courseID uint) (*model.Course, error) {
	if userID == 0 {
		return nil, response.ErrForbidden()
	}

	// 1. 查询课程是否存在
	course, err := s.courseRepo.GetByID(ctx, courseID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.ErrNotFoundWithMsg("课程不存在")
		}
		return nil, fmt.Errorf("get course: %w", err)
	}

	// 2. 校验归属权限
	if course.UserID != userID {
		return nil, response.ErrForbidden()
	}

	return course, nil
}

// CopyFromUser 从指定用户复制全部课程到当前用户（覆盖）
func (s *ScheduleService) CopyFromUser(ctx context.Context, currentUserID, sourceUserID uint) (int, error) {
	// 1. 参数验证
	if currentUserID == 0 {
		return 0, response.ErrForbidden()
	}
	if sourceUserID == 0 {
		return 0, response.ErrInvalidParamWithMsg("源用户ID不能为空")
	}
	if currentUserID == sourceUserID {
		return 0, response.ErrInvalidParamWithMsg("不能复制自己的课表")
	}

	// 2. 验证源用户存在（同租户校验由 GORM 插件自动处理）
	_, err := s.userRepo.FindByID(ctx, sourceUserID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return 0, response.ErrNotFoundWithMsg("源用户不存在")
		}
		return 0, fmt.Errorf("查询源用户失败: %w", err)
	}

	// 3. 查询源用户的全部课程
	sourceCourses, err := s.courseRepo.ListByUser(ctx, sourceUserID)
	if err != nil {
		return 0, fmt.Errorf("查询源课程失败: %w", err)
	}

	if len(sourceCourses) == 0 {
		return 0, response.ErrInvalidParamWithMsg("源用户没有课程")
	}

	// 4. 获取当前激活学期
	var semesterID *uint
	if semester, err := s.semesterRepo.GetActiveSemester(ctx); err == nil && semester != nil {
		semesterID = &semester.ID
	}

	// 5. 深拷贝课程数据（修改归属为当前用户）
	copiedCourses := make([]model.Course, 0, len(sourceCourses))
	for _, c := range sourceCourses {
		copiedCourses = append(copiedCourses, model.Course{
			// ID 不设置，让数据库自增
			UserID:     currentUserID,
			SemesterID: semesterID,
			CourseName: c.CourseName,
			Teacher:    c.Teacher,
			Location:   c.Location,
			DayOfWeek:  c.DayOfWeek,
			Section:    c.Section,
			WeekList:   c.WeekList,
		})
	}

	// 6. 替换当前用户的全部课程（事务：先删后插）
	if err := s.courseRepo.ReplaceByUser(ctx, currentUserID, copiedCourses); err != nil {
		return 0, fmt.Errorf("替换课程失败: %w", err)
	}

	return len(copiedCourses), nil
}

// resolveTargetUserID 校验访问权限并返回实际查询的用户ID
func (s *ScheduleService) resolveTargetUserID(ctx context.Context, viewerID uint, viewerRole int, targetUserID uint) (uint, error) {
	if viewerID == 0 {
		return 0, response.ErrForbidden()
	}

	// 未指定目标或目标即为自己
	if targetUserID == 0 || targetUserID == viewerID {
		return viewerID, nil
	}

	// 管理员及以上可直接查看任意用户
	if viewerRole >= consts.RoleAdmin {
		return targetUserID, nil
	}

	// 普通成员无权查看他人
	return 0, response.ErrForbidden()
}

// hasDeptIntersection 判断两个部门ID列表是否有交集
func hasDeptIntersection(a, b []int64) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	set := make(map[int64]struct{}, len(a))
	for _, id := range a {
		set[id] = struct{}{}
	}

	for _, id := range b {
		if _, ok := set[id]; ok {
			return true
		}
	}
	return false
}
