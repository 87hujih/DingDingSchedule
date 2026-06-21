package handler

import (
	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// RestDayHandler 用户休息日处理器
type RestDayHandler struct {
	restDaySrv *service.RestDayService
}

func NewRestDayHandler(restDaySrv *service.RestDayService) *RestDayHandler {
	return &RestDayHandler{restDaySrv: restDaySrv}
}

// GetMyRestDay 查询当前用户休息日
// GET /api/rest-day/mine
func (h *RestDayHandler) GetMyRestDay(c *gin.Context) {
	userID, _, ok := getViewerAuth(c)
	if !ok {
		return
	}

	resp, err := h.restDaySrv.GetMyRestDay(c.Request.Context(), userID)
	if err != nil {
		response.FailWithError(c, err)
		return
	}
	response.OK(c, resp)
}

// SetMyRestDay 设置/更新当前用户休息日
// PUT /api/rest-day/mine
func (h *RestDayHandler) SetMyRestDay(c *gin.Context) {
	userID, _, ok := getViewerAuth(c)
	if !ok {
		return
	}

	var req dto.SetRestDayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeInvalidParam, "请求参数错误，day_of_week 必须在 1-7 之间")
		return
	}

	if err := h.restDaySrv.SetRestDay(c.Request.Context(), userID, req.DayOfWeek); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OK(c, gin.H{
		"day_of_week": req.DayOfWeek,
		"day_name":    dto.DayOfWeekName(req.DayOfWeek),
	})
}

// RemoveMyRestDay 取消当前用户休息日
// DELETE /api/rest-day/mine
func (h *RestDayHandler) RemoveMyRestDay(c *gin.Context) {
	userID, _, ok := getViewerAuth(c)
	if !ok {
		return
	}

	if err := h.restDaySrv.RemoveRestDay(c.Request.Context(), userID); err != nil {
		response.FailWithError(c, err)
		return
	}

	response.OK(c, gin.H{"message": "已取消休息日"})
}

// ListAllRestDays 管理员查看所有用户休息日
// GET /api/rest-day/users
func (h *RestDayHandler) ListAllRestDays(c *gin.Context) {
	resp, err := h.restDaySrv.ListAllRestDays(c.Request.Context())
	if err != nil {
		response.FailWithError(c, err)
		return
	}
	response.OK(c, resp)
}
