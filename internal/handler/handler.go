package handler

import "schedule_server/internal/service"

// Handler API 处理器集合
type Handler struct {
	UserHdl     *UserHandler
	DeptHdl     *DepartmentHandler
	AuthHdl     *AuthHandler
	ScheduleHdl *ScheduleHandler
	SemesterHdl *SemesterHandler
}

// NewHandler 创建 API 处理器集合
func NewHandler(svc *service.Service) *Handler {
	return &Handler{
		UserHdl:     NewUserHandler(svc.UserSrv),
		DeptHdl:     NewDepartmentHandler(svc.DeptSrv),
		AuthHdl:     NewAuthHandler(svc.AuthSrv),
		ScheduleHdl: NewScheduleHandler(svc.ScheduleSrv),
		SemesterHdl: NewSemesterHandler(svc.SemesterSrv),
	}
}
