package handler

import "schedule_server/internal/service"

// Handler API 处理器集合
type Handler struct {
	UserHdl *UserHandler
}

// NewHandler 创建 API 处理器集合
func NewHandler(svc *service.Service) *Handler {
	return &Handler{
		UserHdl: NewUserHandler(svc.UserSrv),
	}
}
