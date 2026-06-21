package handler

import (
	"time"

	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

// SemesterHandler 学期处理器
type SemesterHandler struct {
	semesterSrv *service.SemesterService
}

func NewSemesterHandler(semesterSrv *service.SemesterService) *SemesterHandler {
	return &SemesterHandler{semesterSrv: semesterSrv}
}

// GetCurrentSemester 获取当前生效学期
func (h *SemesterHandler) GetCurrentSemester(ctx *gin.Context) {
	semester, err := h.semesterSrv.GetActiveSemester(ctx.Request.Context())
	if err != nil {
		response.FailWithError(ctx, response.NewBizError(response.CodeNotFound, "当前无激活学期"))
		return
	}
	response.OK(ctx, semester)
}

// ValidateWeekDate 校验周数与日期是否一致（供其他 handler 调用）
func (h *SemesterHandler) ValidateWeekDate(ctx *gin.Context, date time.Time, week int) error {
	return h.semesterSrv.ValidateWeekDate(ctx.Request.Context(), date, week)
}
