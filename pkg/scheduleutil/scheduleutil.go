package scheduleutil

import (
	"fmt"
	"time"

	"schedule_server/config"
)

// WeekdayMon1Sun7 将 Go 的 Weekday (Sunday=0) 转换为 周一=1, 周日=7 的格式
func WeekdayMon1Sun7(date time.Time) int {
	wd := date.Weekday()
	if wd == time.Sunday {
		return 7
	}
	return int(wd)
}

// CalculateSlotTime 根据日期与节次计算该课节的实际起止时间
// 返回: slotStart, slotEnd, error
func CalculateSlotTime(date time.Time, section int, periods []config.Period) (time.Time, time.Time, error) {
	if date.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("日期不能为空")
	}
	if section <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("节次无效")
	}
	if len(periods) == 0 || section > len(periods) {
		return time.Time{}, time.Time{}, fmt.Errorf("节次作息配置缺失")
	}

	period := periods[section-1]
	startClock, err := time.Parse("15:04", period.Start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("节次作息配置开始时间格式错误")
	}
	endClock, err := time.Parse("15:04", period.End)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("节次作息配置结束时间格式错误")
	}

	loc := date.Location()
	slotStart := time.Date(date.Year(), date.Month(), date.Day(), startClock.Hour(), startClock.Minute(), 0, 0, loc)
	slotEnd := time.Date(date.Year(), date.Month(), date.Day(), endClock.Hour(), endClock.Minute(), 0, 0, loc)

	// 处理跨天情况
	if slotEnd.Before(slotStart) {
		slotEnd = slotEnd.Add(24 * time.Hour)
	}

	return slotStart, slotEnd, nil
}

// TimeOverlaps 判断两个时间区间是否重叠（半开区间 [start, end)）
func TimeOverlaps(startA, endA, startB, endB time.Time) bool {
	return startA.Before(endB) && endA.After(startB)
}
