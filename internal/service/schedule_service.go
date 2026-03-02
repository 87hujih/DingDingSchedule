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
	"schedule_server/internal/errs"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/scheduleparse"
	"schedule_server/pkg/weekutil"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ScheduleService 课表服务
type ScheduleService struct {
	userRepo            repository.UserRepository
	courseRepo          repository.CourseRepository
	semesterRepo        repository.SemesterRepository
	scheduleSettingRepo repository.ScheduleSettingRepository
	dingMgr             *DingTalkClientManager
	logger              *zap.SugaredLogger
}

// NewScheduleService 创建课表服务
func NewScheduleService(
	courseRepo repository.CourseRepository,
	userRepo repository.UserRepository,
	semesterRepo repository.SemesterRepository,
	scheduleSettingRepo repository.ScheduleSettingRepository,
	dingMgr *DingTalkClientManager,
	logger *zap.SugaredLogger,
) *ScheduleService {
	return &ScheduleService{
		userRepo:            userRepo,
		courseRepo:          courseRepo,
		semesterRepo:        semesterRepo,
		scheduleSettingRepo: scheduleSettingRepo,
		dingMgr:             dingMgr,
		logger:              logger,
	}
}

// ImportFromFile 导入课表（覆盖同一用户全部数据），返回插入条数
func (s *ScheduleService) ImportFromFile(ctx context.Context, userID uint, userRole int, srcPath string) (int, error) {
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
		return 0, fmt.Errorf("创建临时xlsx文件失败: %w", err)
	}
	tmpXlsxPath := tmpXlsx.Name()
	_ = tmpXlsx.Close()
	defer os.Remove(tmpXlsxPath)

	if err = scheduleparse.ConvertToXLSX(ctx, srcPath, tmpXlsxPath); err != nil {
		return 0, fmt.Errorf("转换为xlsx格式失败: %w", err)
	}

	parsedCourses, err := scheduleparse.ParseCourses(ctx, tmpXlsxPath)
	if err != nil {
		return 0, fmt.Errorf("解析课程数据失败: %w", err)
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
		return 0, fmt.Errorf("替换课程失败: %w", err)
	}

	s.sendScheduleChangeNotification(ctx, userID, userRole, "导入", fmt.Sprintf("导入了 %d 门课程", len(courses)))

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
		return nil, fmt.Errorf("查询课程列表失败: %w", err)
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
		return nil, fmt.Errorf("查询课程列表失败: %w", err)
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
func (s *ScheduleService) CreateCourse(ctx context.Context, userID uint, userRole int, req *dto.CreateCourseRequest) (uint, error) {
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

	s.sendScheduleChangeNotification(ctx, userID, userRole, "创建", fmt.Sprintf("课程: %s", req.CourseName))

	return course.ID, nil
}

// UpdateCourse 更新课程，校验归属权限
func (s *ScheduleService) UpdateCourse(ctx context.Context, userID uint, userRole int, courseID uint, req *dto.UpdateCourseRequest) error {
	if userID == 0 {
		return response.ErrForbidden()
	}

	// 1. 查询课程是否存在
	existing, err := s.courseRepo.GetByID(ctx, courseID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.ErrNotFoundWithMsg("课程不存在")
		}
		return fmt.Errorf("查询课程失败: %w", err)
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
	if err := s.courseRepo.Update(ctx, updates); err != nil {
		return err
	}

	s.sendScheduleChangeNotification(ctx, userID, userRole, "更新", fmt.Sprintf("课程: %s", req.CourseName))

	return nil
}

// DeleteCourse 删除课程，校验归属权限
func (s *ScheduleService) DeleteCourse(ctx context.Context, userID uint, userRole int, courseID uint) error {
	if userID == 0 {
		return response.ErrForbidden()
	}

	// 1. 查询课程是否存在
	existing, err := s.courseRepo.GetByID(ctx, courseID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.ErrNotFoundWithMsg("课程不存在")
		}
		return fmt.Errorf("查询课程失败: %w", err)
	}

	// 2. 校验归属权限
	if existing.UserID != userID {
		return response.ErrForbidden()
	}

	// 3. 执行删除
	if err := s.courseRepo.Delete(ctx, courseID); err != nil {
		return err
	}

	s.sendScheduleChangeNotification(ctx, userID, userRole, "删除", fmt.Sprintf("课程: %s", existing.CourseName))

	return nil
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
		return nil, fmt.Errorf("查询课程失败: %w", err)
	}

	// 2. 校验归属权限
	if course.UserID != userID {
		return nil, response.ErrForbidden()
	}

	return course, nil
}

// CopyFromUser 从指定用户复制全部课程到当前用户（覆盖）
func (s *ScheduleService) CopyFromUser(ctx context.Context, currentUserID uint, currentUserRole int, sourceUserID uint) (int, error) {
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
	sourceUser, err := s.userRepo.FindByID(ctx, sourceUserID)
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

	s.sendScheduleChangeNotification(ctx, currentUserID, currentUserRole, "复制", fmt.Sprintf("从 %s 复制了 %d 门课程", sourceUser.Name, len(copiedCourses)))

	return len(copiedCourses), nil
}

// DeleteAllCoursesByUser 管理员删除指定用户的全部课程
func (s *ScheduleService) DeleteAllCoursesByUser(ctx context.Context, targetUserID uint) error {
	if targetUserID == 0 {
		return response.ErrInvalidParamWithMsg("用户ID不能为空")
	}

	if err := s.courseRepo.DeleteByUser(ctx, targetUserID); err != nil {
		return fmt.Errorf("删除课程失败: %w", err)
	}

	return nil
}

// resolveTargetUserID 校验访问权限并返回实际查询的用户ID
func (s *ScheduleService) resolveTargetUserID(ctx context.Context, viewerID uint, viewerRole int, targetUserID uint) (uint, error) {
	if viewerID == 0 {
		return 0, response.ErrForbidden()
	}

	// 未指定目标则查自己
	if targetUserID == 0 {
		return viewerID, nil
	}

	// 任何已登录用户均可查看任意用户课表
	return targetUserID, nil
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

// sendScheduleChangeNotification 异步发送课表变更通知
func (s *ScheduleService) sendScheduleChangeNotification(
	ctx context.Context,
	operatorID uint,
	operatorRole int,
	changeType string,
	details string,
) {
	go func() {
		bgCtx := context.Background()
		if tenantID, ok := tenantctx.TenantIDFrom(ctx); ok {
			bgCtx = tenantctx.WithTenantID(bgCtx, tenantID)
		}

		if err := s.doSendScheduleChangeNotification(bgCtx, operatorID, operatorRole, changeType, details); err != nil {
			if s.logger != nil {
				s.logger.Warnw("发送课表变更通知失败",
					"operator_id", operatorID,
					"change_type", changeType,
					"error", err,
				)
			}
		}
	}()
}

// doSendScheduleChangeNotification 实际发送通知的逻辑
func (s *ScheduleService) doSendScheduleChangeNotification(
	ctx context.Context,
	operatorID uint,
	operatorRole int,
	changeType string,
	details string,
) error {
	if s.scheduleSettingRepo != nil {
		enabled, _ := s.scheduleSettingRepo.IsScheduleChangeNotifyEnabled(ctx)
		if !enabled {
			return nil
		}
	}

	if s.dingMgr == nil {
		return fmt.Errorf("钉钉客户端管理器未初始化")
	}

	tenant, dingClient, err := s.dingMgr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("获取钉钉客户端失败: %w", err)
	}

	if tenant.AgentID == "" {
		return fmt.Errorf("钉钉 AgentID 未配置")
	}

	operator, err := s.userRepo.FindByID(ctx, operatorID)
	if err != nil {
		return fmt.Errorf("查询操作者信息失败: %w", err)
	}

	var targetUsers []model.User
	if operatorRole >= consts.RoleAdmin {
		targetUsers = []model.User{*operator}
	} else {
		admins, err := s.userRepo.ListByRole(ctx, consts.RoleAdmin)
		if err != nil {
			return fmt.Errorf("查询管理员列表失败: %w", err)
		}

		userMap := make(map[uint]model.User)
		userMap[operator.ID] = *operator
		for _, admin := range admins {
			userMap[admin.ID] = admin
		}

		targetUsers = make([]model.User, 0, len(userMap))
		for _, user := range userMap {
			targetUsers = append(targetUsers, user)
		}
	}

	dingUserIDs := make([]string, 0, len(targetUsers))
	for _, user := range targetUsers {
		if user.DingUserID != "" {
			dingUserIDs = append(dingUserIDs, user.DingUserID)
		}
	}

	if len(dingUserIDs) == 0 {
		return nil
	}

	content := fmt.Sprintf("【课表变更通知】\n用户 %s 进行了课表%s操作", operator.Name, changeType)
	if details != "" {
		content += fmt.Sprintf("\n详情: %s", details)
	}

	if err := dingClient.SendWorkNoticeText(ctx, tenant.AgentID, dingUserIDs, content); err != nil {
		return errs.WrapMsgErr("发送钉钉通知失败", err)
	}

	return nil
}
