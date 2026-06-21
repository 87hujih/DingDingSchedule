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
	// 获取当前模式和季节，确定要查询的 period mode
	currentMode, _ := s.GetCurrentMode(ctx)
	periodMode := s.resolveSchoolPeriodMode(ctx, currentMode)

	dbPeriods, err := s.periodRepo.ListActiveByMode(ctx, periodMode)
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

// GetPeriodsByMode 获取指定模式的作息时间配置（mode 为 schedule_periods.mode 值，如 school_summer/school_winter/holiday）
func (s *SchedulePeriodService) GetPeriodsByMode(ctx context.Context, mode string) ([]config.Period, error) {
	if mode != model.SchedulePeriodModeSchoolSummer && mode != model.SchedulePeriodModeSchoolWinter && mode != model.SchedulePeriodModeHoliday {
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

// SwitchMode 切换作息模式（school/holiday）
func (s *SchedulePeriodService) SwitchMode(ctx context.Context, mode string) error {
	if mode != model.ScheduleModeSchool && mode != model.ScheduleModeHoliday {
		return response.ErrInvalidParamWithMsg("无效的作息模式，可选值: school, holiday")
	}

	// 检查目标模式是否有配置
	periodMode := s.resolveSchoolPeriodMode(ctx, mode)
	periods, err := s.periodRepo.ListActiveByMode(ctx, periodMode)
	if err != nil {
		return err
	}
	if len(periods) == 0 {
		return response.ErrInvalidParamWithMsg("目标模式暂无作息配置，请先添加配置")
	}

	return s.settingRepo.SwitchMode(ctx, mode)
}

// SwitchSeason 切换上学模式的季节（summer/winter）
func (s *SchedulePeriodService) SwitchSeason(ctx context.Context, season string) error {
	if season != model.SchoolSeasonSummer && season != model.SchoolSeasonWinter {
		return response.ErrInvalidParamWithMsg("无效的季节，可选值: summer, winter")
	}

	// 检查目标季节是否有配置
	periodMode := s.seasonToPeriodMode(season)
	periods, err := s.periodRepo.ListActiveByMode(ctx, periodMode)
	if err != nil {
		return err
	}
	if len(periods) == 0 {
		return response.ErrInvalidParamWithMsg("目标季节暂无作息配置，请先添加配置")
	}

	return s.settingRepo.SwitchSeason(ctx, season)
}

// GetCurrentSeason 获取当前上学模式的季节
func (s *SchedulePeriodService) GetCurrentSeason(ctx context.Context) (string, error) {
	setting, err := s.settingRepo.GetByTenantID(ctx)
	if err != nil {
		return model.SchoolSeasonWinter, nil // 默认冬季
	}
	if setting.SchoolSeason == "" {
		return model.SchoolSeasonWinter, nil
	}
	return setting.SchoolSeason, nil
}

// seasonToPeriodMode 将季节转换为 schedule_periods.mode 值
func (s *SchedulePeriodService) seasonToPeriodMode(season string) string {
	if season == model.SchoolSeasonSummer {
		return model.SchedulePeriodModeSchoolSummer
	}
	return model.SchedulePeriodModeSchoolWinter
}

// resolveSchoolPeriodMode 解析上学模式对应的 period mode（根据当前季节设置）
func (s *SchedulePeriodService) resolveSchoolPeriodMode(ctx context.Context, mode string) string {
	if mode == model.ScheduleModeHoliday {
		return model.SchedulePeriodModeHoliday
	}
	// school 模式：根据当前季节设置返回对应的 period mode
	season, _ := s.GetCurrentSeason(ctx)
	return s.seasonToPeriodMode(season)
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
	currentSeason := model.SchoolSeasonWinter
	attendanceEnabled := true
	restDayEditingAllowed := true
	restDayAttendanceEnabled := true
	if setting != nil {
		currentMode = setting.CurrentMode
		if setting.SchoolSeason != "" {
			currentSeason = setting.SchoolSeason
		}
		attendanceEnabled = setting.AttendanceEnabled
		restDayEditingAllowed = setting.RestDayEditingAllowed
		restDayAttendanceEnabled = setting.RestDayAttendanceEnabled
	}

	schoolSummerPeriods, _ := s.GetPeriodsByMode(ctx, model.SchedulePeriodModeSchoolSummer)
	schoolWinterPeriods, _ := s.GetPeriodsByMode(ctx, model.SchedulePeriodModeSchoolWinter)
	holidayPeriods, _ := s.GetPeriodsByMode(ctx, model.SchedulePeriodModeHoliday)
	activePeriods, _ := s.GetActivePeriods(ctx)

	return &ScheduleInfo{
		CurrentMode:              currentMode,
		SchoolSeason:             currentSeason,
		AttendanceEnabled:        attendanceEnabled,
		RestDayEditingAllowed:    restDayEditingAllowed,
		RestDayAttendanceEnabled: restDayAttendanceEnabled,
		ActivePeriods:            activePeriods,
		SchoolSummerPeriods:      schoolSummerPeriods,
		SchoolWinterPeriods:      schoolWinterPeriods,
		HolidayPeriods:           holidayPeriods,
	}, nil
}

// ScheduleInfo 作息配置完整信息
type ScheduleInfo struct {
	CurrentMode              string          `json:"current_mode"`                // 当前模式: school/holiday
	SchoolSeason             string          `json:"school_season"`               // 上学模式的季节: summer/winter
	AttendanceEnabled        bool            `json:"attendance_enabled"`          // 考勤总开关
	RestDayEditingAllowed    bool            `json:"rest_day_editing_allowed"`    // 休息日编辑开关
	RestDayAttendanceEnabled bool            `json:"rest_day_attendance_enabled"` // 休息日是否参与考勤
	ActivePeriods            []config.Period `json:"active_periods"`              // 当前生效的配置
	SchoolSummerPeriods      []config.Period `json:"school_summer_periods"`       // 夏季上学配置
	SchoolWinterPeriods      []config.Period `json:"school_winter_periods"`       // 冬季上学配置
	HolidayPeriods           []config.Period `json:"holiday_periods"`             // 假期配置
}

// SetRestDayEditingAllowed 设置休息日编辑权限开关
func (s *SchedulePeriodService) SetRestDayEditingAllowed(ctx context.Context, allowed bool) error {
	return s.settingRepo.SetRestDayEditingAllowed(ctx, allowed)
}

// IsRestDayEditingAllowed 检查休息日编辑权限是否启用
func (s *SchedulePeriodService) IsRestDayEditingAllowed(ctx context.Context) (bool, error) {
	return s.settingRepo.IsRestDayEditingAllowed(ctx)
}

// SetRestDayAttendanceEnabled 设置休息日是否参与考勤
func (s *SchedulePeriodService) SetRestDayAttendanceEnabled(ctx context.Context, enabled bool) error {
	return s.settingRepo.SetRestDayAttendanceEnabled(ctx, enabled)
}

// IsRestDayAttendanceEnabled 检查休息日是否参与考勤
func (s *SchedulePeriodService) IsRestDayAttendanceEnabled(ctx context.Context) (bool, error) {
	return s.settingRepo.IsRestDayAttendanceEnabled(ctx)
}

// trimTimeSeconds 去掉时间的秒部分
// "08:00:00" -> "08:00"
func trimTimeSeconds(timeStr string) string {
	if len(timeStr) >= 5 {
		return timeStr[:5]
	}
	return timeStr
}
