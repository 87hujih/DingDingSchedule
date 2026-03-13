package repository

import "gorm.io/gorm"

// Repository 仓库层集合
type Repository struct {
	UserRepo             UserRepository
	DeptRepo             DepartmentRepository
	CourseRepo           CourseRepository
	TenantRepo           TenantRepository
	LeaveRepo            LeaveApprovalRepository
	SemesterRepo         SemesterRepository
	AttendanceRecordRepo AttendanceRecordRepository
	SchedulePeriodRepo   SchedulePeriodRepository
	ScheduleSettingRepo  ScheduleSettingRepository
	AuditLogRepo         AuditLogRepository
	UserRestDayRepo      UserRestDayRepository
	GroupSubRepo         GroupAttendanceSubscriptionRepository
}

// NewRepository 创建仓库实例
func NewRepository(db *gorm.DB) *Repository {
	scheduleSettingRepo := NewScheduleSettingRepository(db)
	return &Repository{
		UserRepo:             NewUserRepository(db),
		DeptRepo:             NewDepartmentRepository(db),
		CourseRepo:           NewCourseRepository(db),
		TenantRepo:           NewTenantRepository(db),
		LeaveRepo:            NewLeaveApprovalRepository(db),
		SemesterRepo:         NewSemesterRepository(db),
		AttendanceRecordRepo: NewAttendanceRecordRepository(db),
		SchedulePeriodRepo:   NewSchedulePeriodRepository(db, scheduleSettingRepo),
		ScheduleSettingRepo:  scheduleSettingRepo,
		AuditLogRepo:         NewAuditLogRepository(db),
		UserRestDayRepo:      NewUserRestDayRepository(db),
		GroupSubRepo:         NewGroupAttendanceSubscriptionRepository(db),
	}
}
