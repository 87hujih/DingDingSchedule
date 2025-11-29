package service

import "schedule_server/internal/repository"

// Service 服务层集合
type Service struct {
	UserSrv *UserService
}

// NewService 创建服务层实例
func NewService(repo *repository.Repository) *Service {
	return &Service{
		UserSrv: NewUserService(repo.UserRepo),
	}
}
