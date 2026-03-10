package service

import (
	"context"

	"schedule_server/config"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"
)

// SchedulePeriodService 作息时间配置服务
type SchedulePeriodService struct {
	periodRepo     repository.SchedulePeriodRepository
	settingRepo    repository.ScheduleSettingRepository
	fallbackConfig *config.Schedule // 配置文件作为回退
}

func NewSchedulePeriodService(
	periodRepo repository.SchedulePeriodRepository,
	settingRepo repository.ScheduleSettingRepository,
	fallbackConfig *config.Schedule,
) *SchedulePeriodService {
	return &SchedulePeriodService{
		periodRepo:     periodRepo,
		settingRepo:    settingRepo,
		fallbackConfig: fallbackConfig,
	}
}

// GetActivePeriods 获取当前生效的作息时间配置
func (s *SchedulePeriodService) GetActivePeriods(ctx context.Context) ([]config.Period, error) {
	dbPeriods, err := s.periodRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}

	if len(dbPeriods) > 0 {
		result := make([]config.Period, len(dbPeriods))
		for i, p := range dbPeriods {
			result[i] = config.Period{
				Name:  p.Name,
				Start: trimTimeSeconds(p.StartTime),
				End:   trimTimeSeconds(p.EndTime),
			}
		}
		return result, nil
	}

	// 回退到配置文件
	if s.fallbackConfig != nil && len(s.fallbackConfig.Periods) > 0 {
		return s.fallbackConfig.Periods, nil
	}

	return []config.Period{}, nil
}

// GetPeriodsByMode 获取指定模式的作息时间配置
func (s *SchedulePeriodService) GetPeriodsByMode(ctx context.Context, mode string) ([]config.Period, error) {
	if mode != model.ScheduleModeSchool && mode != model.ScheduleModeHoliday {
		return nil, response.ErrInvalidParamWithMsg("无效的作息模式")
	}

	dbPeriods, err := s.periodRepo.ListActiveByMode(ctx, mode)
	if err != nil {
		return nil, err
	}

	result := make([]config.Period, len(dbPeriods))
	for i, p := range dbPeriods {
		result[i] = config.Period{
			Name:  p.Name,
			Start: trimTimeSeconds(p.StartTime),
			End:   trimTimeSeconds(p.EndTime),
		}
	}
	return result, nil
}

// GetCurrentMode 获取当前作息模式
func (s *SchedulePeriodService) GetCurrentMode(ctx context.Context) (string, error) {
	setting, err := s.settingRepo.GetByTenantID(ctx)
	if err != nil {
		// 默认返回上学模式
		return model.ScheduleModeSchool, nil
	}
	return setting.CurrentMode, nil
}

// SwitchMode 切换作息模式
func (s *SchedulePeriodService) SwitchMode(ctx context.Context, mode string) error {
	if mode != model.ScheduleModeSchool && mode != model.ScheduleModeHoliday {
		return response.ErrInvalidParamWithMsg("无效的作息模式，可选值: school, holiday")
	}

	// 检查目标模式是否有配置
	periods, err := s.periodRepo.ListActiveByMode(ctx, mode)
	if err != nil {
		return err
	}
	if len(periods) == 0 {
		return response.ErrInvalidParamWithMsg("目标模式暂无作息配置，请先添加配置")
	}

	return s.settingRepo.SwitchMode(ctx, mode)
}

// SetAttendanceEnabled 设置考勤开关状态
func (s *SchedulePeriodService) SetAttendanceEnabled(ctx context.Context, enabled bool) error {
	return s.settingRepo.SetAttendanceEnabled(ctx, enabled)
}

// IsAttendanceEnabled 检查考勤是否启用
func (s *SchedulePeriodService) IsAttendanceEnabled(ctx context.Context) (bool, error) {
	return s.settingRepo.IsAttendanceEnabled(ctx)
}

// SetScheduleChangeNotifyEnabled 设置课表变更通知开关
func (s *SchedulePeriodService) SetScheduleChangeNotifyEnabled(ctx context.Context, enabled bool) error {
	return s.settingRepo.SetScheduleChangeNotifyEnabled(ctx, enabled)
}

// IsScheduleChangeNotifyEnabled 检查课表变更通知是否启用
func (s *SchedulePeriodService) IsScheduleChangeNotifyEnabled(ctx context.Context) (bool, error) {
	return s.settingRepo.IsScheduleChangeNotifyEnabled(ctx)
}

// SetLateNotifyEnabled 设置迟到提醒通知开关
func (s *SchedulePeriodService) SetLateNotifyEnabled(ctx context.Context, enabled bool) error {
	return s.settingRepo.SetLateNotifyEnabled(ctx, enabled)
}

// IsLateNotifyEnabled 检查迟到提醒通知是否启用
func (s *SchedulePeriodService) IsLateNotifyEnabled(ctx context.Context) (bool, error) {
	return s.settingRepo.IsLateNotifyEnabled(ctx)
}

// GetScheduleInfo 获取完整的作息配置信息
func (s *SchedulePeriodService) GetScheduleInfo(ctx context.Context) (*ScheduleInfo, error) {
	setting, _ := s.settingRepo.GetByTenantID(ctx)
	currentMode := model.ScheduleModeSchool
	attendanceEnabled := true
	restDayEditingAllowed := true
	if setting != nil {
		currentMode = setting.CurrentMode
		attendanceEnabled = setting.AttendanceEnabled
		restDayEditingAllowed = setting.RestDayEditingAllowed
	}

	schoolPeriods, _ := s.GetPeriodsByMode(ctx, model.ScheduleModeSchool)
	holidayPeriods, _ := s.GetPeriodsByMode(ctx, model.ScheduleModeHoliday)
	activePeriods, _ := s.GetActivePeriods(ctx)

	return &ScheduleInfo{
		CurrentMode:           currentMode,
		AttendanceEnabled:     attendanceEnabled,
		RestDayEditingAllowed: restDayEditingAllowed,
		ActivePeriods:         activePeriods,
		SchoolPeriods:         schoolPeriods,
		HolidayPeriods:        holidayPeriods,
	}, nil
}

// ScheduleInfo 作息配置完整信息
type ScheduleInfo struct {
	CurrentMode           string          `json:"current_mode"`             // 当前模式
	AttendanceEnabled     bool            `json:"attendance_enabled"`       // 考勤总开关
	RestDayEditingAllowed bool            `json:"rest_day_editing_allowed"` // 休息日编辑开关
	ActivePeriods         []config.Period `json:"active_periods"`           // 当前生效的配置
	SchoolPeriods         []config.Period `json:"school_periods"`           // 上学配置
	HolidayPeriods        []config.Period `json:"holiday_periods"`          // 假期配置
}

// SetRestDayEditingAllowed 设置休息日编辑权限开关
func (s *SchedulePeriodService) SetRestDayEditingAllowed(ctx context.Context, allowed bool) error {
	return s.settingRepo.SetRestDayEditingAllowed(ctx, allowed)
}

// IsRestDayEditingAllowed 检查休息日编辑权限是否启用
func (s *SchedulePeriodService) IsRestDayEditingAllowed(ctx context.Context) (bool, error) {
	return s.settingRepo.IsRestDayEditingAllowed(ctx)
}

// trimTimeSeconds 去掉时间的秒部分
// "08:00:00" -> "08:00"
func trimTimeSeconds(timeStr string) string {
	if len(timeStr) >= 5 {
		return timeStr[:5]
	}
	return timeStr
}
