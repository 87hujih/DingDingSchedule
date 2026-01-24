package handler

import (
	"schedule_server/internal/repository"
	"schedule_server/internal/service"
)

// Handler API 处理器集合
type Handler struct {
	UserHdl             *UserHandler
	DeptHdl             *DepartmentHandler
	AuthHdl             *AuthHandler
	ScheduleHdl         *ScheduleHandler
	AttendanceHdl       *AttendanceHandler
	SemesterHdl         *SemesterHandler
	AttendanceRecordHdl *AttendanceRecordHandler
	ScheduleSettingHdl  *ScheduleSettingHandler
}

// NewHandler 创建 API 处理器集合
func NewHandler(svc *service.Service, repo *repository.Repository) *Handler {
	return &Handler{
		UserHdl:             NewUserHandler(svc.UserSrv, repo.TenantRepo),
		DeptHdl:             NewDepartmentHandler(svc.DeptSrv),
		AuthHdl:             NewAuthHandler(svc.AuthSrv),
		ScheduleHdl:         NewScheduleHandler(svc.ScheduleSrv),
		AttendanceHdl:       NewAttendanceHandler(svc.AttendanceSrv, svc.SemesterSrv),
		SemesterHdl:         NewSemesterHandler(svc.SemesterSrv),
		AttendanceRecordHdl: NewAttendanceRecordHandler(svc.AttendanceRecordSrv, svc.SemesterSrv),
		ScheduleSettingHdl:  NewScheduleSettingHandler(svc.SchedulePeriodSrv),
	}
}
