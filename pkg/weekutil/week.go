package weekutil

import "time"

// CurrentWeek 根据起始日期计算当前是第几周
// 若当前时间早于起始日期，返回 1；超过 totalWeek 则返回 totalWeek
func CurrentWeek(startDate time.Time, totalWeek int) int {
	now := time.Now()
	if now.Before(startDate) {
		return 1
	}

	days := int(now.Sub(startDate).Hours() / 24)
	week := days/7 + 1

	if week > totalWeek {
		return totalWeek
	}
	return week
}

// ContainsWeek 检查周次列表字符串是否包含指定周次
// weekList 格式为逗号分隔的整数，如 "1,2,3,4,5"
func ContainsWeek(weekList string, week int) bool {
	if weekList == "" || week <= 0 {
		return false
	}

	// 手动解析，避免引入额外依赖
	start := 0
	for i := 0; i <= len(weekList); i++ {
		if i == len(weekList) || weekList[i] == ',' {
			if start < i {
				n := 0
				for j := start; j < i; j++ {
					c := weekList[j]
					if c >= '0' && c <= '9' {
						n = n*10 + int(c-'0')
					}
				}
				if n == week {
					return true
				}
			}
			start = i + 1
		}
	}
	return false
}
