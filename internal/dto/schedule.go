package dto

import (
	"schedule_server/internal/model"
	"time"
)

// ImportScheduleResponse 导入课表结果
type ImportScheduleResponse struct {
	Inserted int `json:"inserted"`
}

// ScheduleItem 课表项
type ScheduleItem struct {
	ID         uint   `json:"id"`
	CourseName string `json:"course_name"`
	Teacher    string `json:"teacher"`
	Location   string `json:"location"`
	DayOfWeek  int    `json:"day_of_week"`
	Section    int    `json:"section"`
	WeekList   string `json:"week_list"`
}

// ScheduleListResponse 课表列表响应（按周查询，不分页）
type ScheduleListResponse struct {
	Week  int            `json:"week"`  // 查询的周次
	Items []ScheduleItem `json:"items"` // 课程列表
}

// ScheduleListParams 构造 ScheduleListResponse 的参数
type ScheduleListParams struct {
	Week    int // 请求的周次
	Courses []model.Course
}

// NewScheduleListResponse 从参数构造课表列表响应
func NewScheduleListResponse(p *ScheduleListParams) *ScheduleListResponse {
	week := p.Week

	items := make([]ScheduleItem, 0, len(p.Courses))
	for _, c := range p.Courses {
		items = append(items, ScheduleItem{
			ID:         c.ID,
			CourseName: c.CourseName,
			Teacher:    c.Teacher,
			Location:   c.Location,
			DayOfWeek:  c.DayOfWeek,
			Section:    c.Section,
			WeekList:   c.WeekList,
		})
	}

	return &ScheduleListResponse{
		Week:  week,
		Items: items,
	}
}

// CreateCourseRequest 手动添加课程请求
type CreateCourseRequest struct {
	CourseName string `json:"course_name" binding:"required"`
	Teacher    string `json:"teacher"`
	Location   string `json:"location"`
	DayOfWeek  int    `json:"day_of_week" binding:"required,min=1,max=7"`
	Section    int    `json:"section" binding:"required,min=1"`
	WeekList   string `json:"week_list" binding:"required"` // 如 "1,2,3,4,5"
}

// UpdateCourseRequest 更新课程请求
type UpdateCourseRequest struct {
	CourseName string `json:"course_name"`
	Teacher    string `json:"teacher"`
	Location   string `json:"location"`
	DayOfWeek  int    `json:"day_of_week" binding:"omitempty,min=1,max=7"`
	Section    int    `json:"section" binding:"omitempty,min=1"`
	WeekList   string `json:"week_list"`
}

// CreateCourseResponse 创建课程响应
type CreateCourseResponse struct {
	ID uint `json:"id"`
}

// AllCoursesListResponse 全部课程列表响应（不按周过滤）
type AllCoursesListResponse struct {
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int            `json:"total"`
	Items    []ScheduleItem `json:"items"`
}

// AllCoursesListParams 构造 AllCoursesListResponse 的参数
type AllCoursesListParams struct {
	Page     int
	PageSize int
	Total    int
	Courses  []model.Course
}

// NewAllCoursesListResponse 从参数构造全部课程列表响应
func NewAllCoursesListResponse(p *AllCoursesListParams) *AllCoursesListResponse {
	page := p.Page
	if page <= 0 {
		page = 1
	}
	pageSize := p.PageSize
	if pageSize <= 0 {
		pageSize = 10
	}

	items := make([]ScheduleItem, 0, len(p.Courses))
	for _, c := range p.Courses {
		items = append(items, ScheduleItem{
			ID:         c.ID,
			CourseName: c.CourseName,
			Teacher:    c.Teacher,
			Location:   c.Location,
			DayOfWeek:  c.DayOfWeek,
			Section:    c.Section,
			WeekList:   c.WeekList,
		})
	}

	return &AllCoursesListResponse{
		Page:     page,
		PageSize: pageSize,
		Total:    p.Total,
		Items:    items,
	}
}

// CourseDetailResponse 课程详情响应
type CourseDetailResponse struct {
	ID         uint   `json:"id"`
	CourseName string `json:"course_name"`
	Teacher    string `json:"teacher"`
	Location   string `json:"location"`
	DayOfWeek  int    `json:"day_of_week"`
	Section    int    `json:"section"`
	WeekList   string `json:"week_list"`
}

// NewCourseDetailResponse 从 model.Course 构造课程详情响应
func NewCourseDetailResponse(c *model.Course) *CourseDetailResponse {
	return &CourseDetailResponse{
		ID:         c.ID,
		CourseName: c.CourseName,
		Teacher:    c.Teacher,
		Location:   c.Location,
		DayOfWeek:  c.DayOfWeek,
		Section:    c.Section,
		WeekList:   c.WeekList,
	}
}

// CopyFromUserRequest 从其他用户复制课表请求
type CopyFromUserRequest struct {
	SourceUserID uint `json:"source_user_id" binding:"required,min=1"`
}

// CopyFromUserResponse 复制课表响应
type CopyFromUserResponse struct {
	Copied int `json:"copied"` // 复制的课程数量
}

// CourseAttendanceUserItem 课节考勤中的用户信息
type CourseAttendanceUserItem struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Avatar   string `json:"avatar"`
	Phone    string `json:"phone"`
	DeptName string `json:"dept_name,omitempty"`
}

// SlotAttendanceStatusResponse 时段考勤状态响应（不依赖课程ID）
type SlotAttendanceStatusResponse struct {
	Date         string                     `json:"date"`
	Week         int                        `json:"week"`
	DayOfWeek    int                        `json:"day_of_week"`
	Section      int                        `json:"section"`
	ShouldArrive []CourseAttendanceUserItem `json:"should_arrive"`
	OnLeave      []CourseAttendanceUserItem `json:"on_leave,omitempty"`
	OnRestDay    []CourseAttendanceUserItem `json:"on_rest_day,omitempty"`
	HasCourse    []CourseAttendanceUserItem `json:"has_course,omitempty"`
}

// CourseLeaveRecordItem 请假记录明细（点击人员后展示）
type CourseLeaveRecordItem struct {
	UserName        string    `json:"user_name"`                  // 请假人姓名
	LeaveType       string    `json:"leave_type"`                 // 请假类型
	StartAt         time.Time `json:"start_at"`                   // 请假开始时间
	EndAt           time.Time `json:"end_at"`                     // 请假结束时间
	DurationSeconds int64     `json:"duration_seconds,omitempty"` // 重叠时长（秒）
	Status          string    `json:"status,omitempty"`           // 审批状态
	Remark          string    `json:"remark,omitempty"`           // 请假理由
}

// SlotUserLeaveDetailResponse 某用户在某“时段(日期+节次)时间窗口”内的请假明细响应（不依赖课程ID）。
type SlotUserLeaveDetailResponse struct {
	UserID       uint                    `json:"user_id"`
	Week         int                     `json:"week"`
	Date         string                  `json:"date"`
	DayOfWeek    int                     `json:"day_of_week"`
	Section      int                     `json:"section"`
	SessionStart time.Time               `json:"session_start"`
	SessionEnd   time.Time               `json:"session_end"`
	Items        []CourseLeaveRecordItem `json:"items"`
}
