package service

import (
	"schedule_server/config"
	"schedule_server/internal/repository"

	"go.uber.org/zap"
)

// Service 服务层集合
type Service struct {
	UserSrv             *UserService
	DeptSrv             *DepartmentService
	AuthSrv             *AuthService
	ScheduleSrv         *ScheduleService
	AttendanceSrv       *AttendanceService
	LeaveSyncSrv        *LeaveSyncService
	SemesterSrv         *SemesterService
	AttendanceRecordSrv *AttendanceRecordService
	SchedulePeriodSrv   *SchedulePeriodService
}

// NewService 创建服务层实例
func NewService(repo *repository.Repository, dingMgr *DingTalkClientManager, jwtCfg config.JWT, scheduleCfg config.Schedule, logger *zap.SugaredLogger) *Service {
	attendanceRepo := repository.NewAttendanceRepository(repo.UserRepo, repo.CourseRepo)

	leaveSyncSrv := NewLeaveSyncService(repo.LeaveRepo, repo.UserRepo, dingMgr, logger)

	schedulePeriodSrv := NewSchedulePeriodService(repo.SchedulePeriodRepo, repo.ScheduleSettingRepo, &scheduleCfg)

	attendanceRecordSrv := NewAttendanceRecordService(
		repo.UserRepo,
		repo.CourseRepo,
		repo.LeaveRepo,
		repo.AttendanceRecordRepo,
		dingMgr,
		schedulePeriodSrv,
		scheduleCfg,
		logger,
	)

	return &Service{
		UserSrv:             NewUserService(repo.UserRepo, dingMgr),
		DeptSrv:             NewDepartmentService(repo.DeptRepo, dingMgr),
		AuthSrv:             NewAuthService(repo.UserRepo, dingMgr, jwtCfg),
		ScheduleSrv:         NewScheduleService(repo.CourseRepo, repo.UserRepo, repo.SemesterRepo),
		AttendanceSrv:       NewAttendanceService(attendanceRepo, dingMgr, scheduleCfg, logger),
		LeaveSyncSrv:        leaveSyncSrv,
		SemesterSrv:         NewSemesterService(repo.SemesterRepo),
		AttendanceRecordSrv: attendanceRecordSrv,
		SchedulePeriodSrv:   schedulePeriodSrv,
	}
}
