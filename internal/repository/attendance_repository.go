package repository

import (
	"context"

	"schedule_server/internal/model"
)

// AttendanceRepository 考勤模块所需的数据访问集合（组合复用现有仓库，不新增表）。
type AttendanceRepository interface {
	// Courses
	GetCourseByID(ctx context.Context, id uint) (*model.Course, error)
	ListCoursesByUsersSemesterDaySection(ctx context.Context, userIDs []uint, semester string, dayOfWeek, section int) ([]model.Course, error)

	// Semesters
	GetSemesterByName(ctx context.Context, name string) (*model.Semester, error)

	// Users
	FindUserByID(ctx context.Context, id uint) (*model.User, error)
	FindUserDepartmentIDs(ctx context.Context, userID uint) ([]int64, error)
	ListUsersByScope(ctx context.Context, deptIDs []int64, onlyUserIDs []uint) ([]model.User, error)
}

type attendanceRepository struct {
	userRepo     UserRepository
	courseRepo   CourseRepository
	semesterRepo SemesterRepository
}

func NewAttendanceRepository(userRepo UserRepository, courseRepo CourseRepository, semesterRepo SemesterRepository) AttendanceRepository {
	return &attendanceRepository{
		userRepo:     userRepo,
		courseRepo:   courseRepo,
		semesterRepo: semesterRepo,
	}
}

func (r *attendanceRepository) GetCourseByID(ctx context.Context, id uint) (*model.Course, error) {
	return r.courseRepo.GetByID(ctx, id)
}

func (r *attendanceRepository) ListCoursesByUsersSemesterDaySection(ctx context.Context, userIDs []uint, semester string, dayOfWeek, section int) ([]model.Course, error) {
	return r.courseRepo.ListByUsersSemesterDaySection(ctx, userIDs, semester, dayOfWeek, section)
}

func (r *attendanceRepository) GetSemesterByName(ctx context.Context, name string) (*model.Semester, error) {
	return r.semesterRepo.GetByName(ctx, name)
}

func (r *attendanceRepository) FindUserByID(ctx context.Context, id uint) (*model.User, error) {
	return r.userRepo.FindByID(ctx, id)
}

func (r *attendanceRepository) FindUserDepartmentIDs(ctx context.Context, userID uint) ([]int64, error) {
	return r.userRepo.FindDepartmentIDs(ctx, userID)
}

func (r *attendanceRepository) ListUsersByScope(ctx context.Context, deptIDs []int64, onlyUserIDs []uint) ([]model.User, error) {
	return r.userRepo.ListByScope(ctx, deptIDs, onlyUserIDs)
}
