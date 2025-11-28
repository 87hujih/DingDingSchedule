package handler

import "schedule_server/internal/service"

// Handler API处理器集合
type Handler struct {
	UserHdl *UserHandler
}

// NewHandler 创建API处理器集合
func NewHandler(srv *service.Service) *Handler {
	return &Handler{
		UserHdl: NewUserHandler(srv),
	}
}
