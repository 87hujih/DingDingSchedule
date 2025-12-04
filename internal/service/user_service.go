package service

import (
	"context"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
)

// UserService 用户服务
type UserService struct {
	userRepo repository.UserRepository
}

// NewUserService 创建用户服务实例
func NewUserService(userRepo repository.UserRepository) *UserService {
	return &UserService{userRepo: userRepo}
}

// GetUserById 获取用户信息
func (s *UserService) GetUserById(ctx context.Context, id uint) (*model.User, error) {
	return s.userRepo.FindByID(ctx, id)
}
