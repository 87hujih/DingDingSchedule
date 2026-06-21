package repository

import "gorm.io/gorm"

// Repository 仓库层集合
type Repository struct {
	UserRepo                     UserRepository
	DeptRepo                     DepartmentRepository
	CourseRepo                   CourseRepository
	TenantRepo                   TenantRepository
	AgentKnowledgeRepo           AgentKnowledgeRepository
	LeaveRepo                    LeaveApprovalRepository
	SemesterRepo                 SemesterRepository
	AttendanceRecordRepo         AttendanceRecordRepository
	AttendanceManualOverrideRepo AttendanceManualOverrideRepository
	SchedulePeriodRepo           SchedulePeriodRepository
	ScheduleSettingRepo          ScheduleSettingRepository
	AuditLogRepo                 AuditLogRepository
	UserRestDayRepo              UserRestDayRepository
	GroupSubRepo                 GroupAttendanceSubscriptionRepository
}

// NewRepository 创建仓库实例
func NewRepository(db *gorm.DB) *Repository {
	scheduleSettingRepo := NewScheduleSettingRepository(db)
	return &Repository{
		UserRepo:                     NewUserRepository(db),
		DeptRepo:                     NewDepartmentRepository(db),
		CourseRepo:                   NewCourseRepository(db),
		TenantRepo:                   NewTenantRepository(db),
		AgentKnowledgeRepo:           NewAgentKnowledgeRepository(db),
		LeaveRepo:                    NewLeaveApprovalRepository(db),
		SemesterRepo:                 NewSemesterRepository(db),
		AttendanceRecordRepo:         NewAttendanceRecordRepository(db),
		AttendanceManualOverrideRepo: NewAttendanceManualOverrideRepository(db),
		SchedulePeriodRepo:           NewSchedulePeriodRepository(db, scheduleSettingRepo),
		ScheduleSettingRepo:          scheduleSettingRepo,
		AuditLogRepo:                 NewAuditLogRepository(db),
		UserRestDayRepo:              NewUserRestDayRepository(db),
		GroupSubRepo:                 NewGroupAttendanceSubscriptionRepository(db),
	}
}
