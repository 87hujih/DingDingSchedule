package service

import (
	"schedule_server/config"
	"schedule_server/internal/repository"
	"schedule_server/pkg/dingtalk"
)

// Service 服务层集合
type Service struct {
	UserSrv     *UserService
	DeptSrv     *DepartmentService
	AuthSrv     *AuthService
	ScheduleSrv *ScheduleService
	SemesterSrv *SemesterService
}

// NewService 创建服务层实例
func NewService(repo *repository.Repository, dingClient *dingtalk.Client, jwtCfg config.JWT) *Service {
	return &Service{
		UserSrv:     NewUserService(repo.UserRepo),
		DeptSrv:     NewDepartmentService(repo.DeptRepo),
		AuthSrv:     NewAuthService(repo.UserRepo, dingClient, jwtCfg),
		ScheduleSrv: NewScheduleService(repo.CourseRepo, repo.SemesterRepo, repo.UserRepo),
		SemesterSrv: NewSemesterService(repo.SemesterRepo),
	}
}
