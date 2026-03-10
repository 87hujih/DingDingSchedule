package service

import (
	"context"
	"errors"

	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// RestDayService 用户休息日服务
type RestDayService struct {
	restDayRepo repository.UserRestDayRepository
	settingRepo repository.ScheduleSettingRepository
	userRepo    repository.UserRepository
	logger      *zap.SugaredLogger
}

func NewRestDayService(
	restDayRepo repository.UserRestDayRepository,
	settingRepo repository.ScheduleSettingRepository,
	userRepo repository.UserRepository,
	logger *zap.SugaredLogger,
) *RestDayService {
	return &RestDayService{
		restDayRepo: restDayRepo,
		settingRepo: settingRepo,
		userRepo:    userRepo,
		logger:      logger,
	}
}

// checkRestDayEditingAllowed 检查休息日编辑权限，DB错误时默认允许（fail-open）
func (s *RestDayService) checkRestDayEditingAllowed(ctx context.Context) error {
	allowed, err := s.settingRepo.IsRestDayEditingAllowed(ctx)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Warnw("检查休息日编辑权限失败，默认允许", "error", err)
		}
		return nil // fail-open: DB错误或设置不存在时默认允许
	}
	if !allowed {
		return response.NewBizError(response.CodeForbidden, "管理员已关闭休息日编辑权限")
	}
	return nil
}

// SetRestDay 用户设置休息日（先检查编辑权限开关）
func (s *RestDayService) SetRestDay(ctx context.Context, userID uint, dayOfWeek int) error {
	if userID == 0 {
		return response.ErrForbidden()
	}
	if dayOfWeek < 1 || dayOfWeek > 7 {
		return response.ErrInvalidParamWithMsg("day_of_week 必须在 1-7 之间")
	}

	if err := s.checkRestDayEditingAllowed(ctx); err != nil {
		return err
	}

	record := &model.UserRestDay{
		UserID:    userID,
		DayOfWeek: &dayOfWeek,
	}
	if err := s.restDayRepo.Upsert(ctx, record); err != nil {
		s.logger.Errorw("设置休息日失败", "userID", userID, "dayOfWeek", dayOfWeek, "error", err)
		return response.NewBizError(response.CodeInternalError, "设置休息日失败")
	}
	return nil
}

// RemoveRestDay 用户取消休息日（先检查编辑权限开关）
func (s *RestDayService) RemoveRestDay(ctx context.Context, userID uint) error {
	if userID == 0 {
		return response.ErrForbidden()
	}

	if err := s.checkRestDayEditingAllowed(ctx); err != nil {
		return err
	}

	if err := s.restDayRepo.Unset(ctx, userID); err != nil {
		s.logger.Errorw("取消休息日失败", "userID", userID, "error", err)
		return response.NewBizError(response.CodeInternalError, "取消休息日失败")
	}
	return nil
}

// GetMyRestDay 查询用户自己的休息日
func (s *RestDayService) GetMyRestDay(ctx context.Context, userID uint) (*dto.UserRestDayResponse, error) {
	if userID == 0 {
		return nil, response.ErrForbidden()
	}

	record, err := s.restDayRepo.FindByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &dto.UserRestDayResponse{
				UserID:    userID,
				DayOfWeek: 0,
				DayName:   "",
			}, nil
		}
		s.logger.Errorw("查询用户休息日失败", "userID", userID, "error", err)
		return nil, response.NewBizError(response.CodeInternalError, "查询休息日失败")
	}

	if record.DayOfWeek == nil {
		return &dto.UserRestDayResponse{
			UserID:    userID,
			DayOfWeek: 0,
			DayName:   "",
		}, nil
	}

	return &dto.UserRestDayResponse{
		UserID:    userID,
		DayOfWeek: *record.DayOfWeek,
		DayName:   dto.DayOfWeekName(*record.DayOfWeek),
	}, nil
}

// ListAllRestDays 管理员查看所有用户休息日
func (s *RestDayService) ListAllRestDays(ctx context.Context) (*dto.UserRestDayListResponse, error) {
	records, err := s.restDayRepo.ListAll(ctx)
	if err != nil {
		s.logger.Errorw("查询所有休息日失败", "error", err)
		return nil, response.NewBizError(response.CodeInternalError, "查询休息日列表失败")
	}

	if len(records) == 0 {
		return &dto.UserRestDayListResponse{Items: []dto.UserRestDayItem{}}, nil
	}

	// 收集用户ID
	userIDs := make([]uint, 0, len(records))
	for _, r := range records {
		userIDs = append(userIDs, r.UserID)
	}

	// 批量查询用户信息
	users, err := s.userRepo.ListByIDs(ctx, userIDs)
	if err != nil {
		s.logger.Errorw("批量查询用户失败", "error", err)
		return nil, response.NewBizError(response.CodeInternalError, "查询用户信息失败")
	}
	userMap := make(map[uint]*model.User, len(users))
	for i := range users {
		userMap[users[i].ID] = &users[i]
	}

	// 批量查询部门名称
	deptNames, err := s.userRepo.GetUserDepartmentNames(ctx, userIDs)
	if err != nil {
		s.logger.Warnw("查询部门名称失败", "error", err)
		deptNames = make(map[uint]string)
	}

	items := make([]dto.UserRestDayItem, 0, len(records))
	for _, r := range records {
		u := userMap[r.UserID]
		if u == nil {
			continue
		}
		items = append(items, dto.UserRestDayItem{
			UserID:    r.UserID,
			UserName:  u.Name,
			Avatar:    u.Avatar,
			DeptName:  deptNames[r.UserID],
			DayOfWeek: *r.DayOfWeek,
			DayName:   dto.DayOfWeekName(*r.DayOfWeek),
		})
	}

	return &dto.UserRestDayListResponse{Items: items}, nil
}
