package repository

import (
	"context"

	"schedule_server/internal/model"
)

// AttendanceRepository 考勤模块所需的数据访问集合
type AttendanceRepository interface {
	ListCoursesByUsersDaySection(ctx context.Context, userIDs []uint, dayOfWeek, section int) ([]model.Course, error)

	// FindUserByID Users
	FindUserByID(ctx context.Context, id uint) (*model.User, error)
	FindUserDepartmentIDs(ctx context.Context, userID uint) ([]int64, error)
	ListUsersByScope(ctx context.Context, deptIDs []int64, onlyUserIDs []uint) ([]model.User, error)
}

// attendanceRepository 复用 user/course 仓库实现 AttendanceRepository
type attendanceRepository struct {
	userRepo   UserRepository
	courseRepo CourseRepository
}

// NewAttendanceRepository 组合用户与课程仓库生成考勤仓库实现
func NewAttendanceRepository(userRepo UserRepository, courseRepo CourseRepository) AttendanceRepository {
	return &attendanceRepository{
		userRepo:   userRepo,
		courseRepo: courseRepo,
	}
}

// ListCoursesByUsersDaySection 查询指定用户/星期/节次所对应的课程
func (r *attendanceRepository) ListCoursesByUsersDaySection(ctx context.Context, userIDs []uint, dayOfWeek, section int) ([]model.Course, error) {
	return r.courseRepo.ListByUsersDaySection(ctx, userIDs, dayOfWeek, section)
}

// FindUserByID 透传到 UserRepository 查询单个用户
func (r *attendanceRepository) FindUserByID(ctx context.Context, id uint) (*model.User, error) {
	return r.userRepo.FindByID(ctx, id)
}

// FindUserDepartmentIDs 返回用户所属部门ID
func (r *attendanceRepository) FindUserDepartmentIDs(ctx context.Context, userID uint) ([]int64, error) {
	return r.userRepo.FindDepartmentIDs(ctx, userID)
}

// ListUsersByScope 根据部门或指定用户范围拉取用户列表
func (r *attendanceRepository) ListUsersByScope(ctx context.Context, deptIDs []int64, onlyUserIDs []uint) ([]model.User, error) {
	return r.userRepo.ListByScope(ctx, deptIDs, onlyUserIDs)
}
