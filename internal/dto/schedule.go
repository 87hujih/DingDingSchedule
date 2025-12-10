package dto

import "schedule_server/internal/model"

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
	Semester   string `json:"semester"`
}

// ScheduleListResponse 课表列表响应（按周查询，不分页）
type ScheduleListResponse struct {
	CurrentWeek int            `json:"current_week"` // 当前周次
	TotalWeek   int            `json:"total_week"`   // 学期总周数
	Week        int            `json:"week"`         // 查询的周次
	Items       []ScheduleItem `json:"items"`        // 课程列表
}

// ScheduleListParams 构造 ScheduleListResponse 的参数
type ScheduleListParams struct {
	CurrentWeek int
	TotalWeek   int
	Week        int // 请求的周次，<=0 时使用 CurrentWeek
	Courses     []model.Course
}

// NewScheduleListResponse 从参数构造课表列表响应
func NewScheduleListResponse(p *ScheduleListParams) *ScheduleListResponse {
	week := p.Week
	if week <= 0 {
		week = p.CurrentWeek
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
			Semester:   c.Semester,
		})
	}

	return &ScheduleListResponse{
		CurrentWeek: p.CurrentWeek,
		TotalWeek:   p.TotalWeek,
		Week:        week,
		Items:       items,
	}
}

// CreateCourseRequest 手动添加课程请求
type CreateCourseRequest struct {
	Semester   string `json:"semester" binding:"required"`
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
			Semester:   c.Semester,
		})
	}

	return &AllCoursesListResponse{
		Page:     page,
		PageSize: pageSize,
		Total:    p.Total,
		Items:    items,
	}
}
