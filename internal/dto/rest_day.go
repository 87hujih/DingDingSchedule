package dto

// SetRestDayRequest 设置休息日请求
type SetRestDayRequest struct {
	DayOfWeek int `json:"day_of_week" binding:"required,min=1,max=7"`
}

// UserRestDayResponse 用户休息日响应
type UserRestDayResponse struct {
	UserID    uint   `json:"user_id"`
	DayOfWeek int    `json:"day_of_week"` // 0表示未设置
	DayName   string `json:"day_name"`    // "周一"~"周日"，未设置为空
}

// UserRestDayItem 用户休息日列表项（管理员视角）
type UserRestDayItem struct {
	UserID    uint   `json:"user_id"`
	UserName  string `json:"user_name"`
	Avatar    string `json:"avatar"`
	DeptName  string `json:"dept_name"`
	DayOfWeek int    `json:"day_of_week"`
	DayName   string `json:"day_name"`
}

// UserRestDayListResponse 用户休息日列表响应
type UserRestDayListResponse struct {
	Items []UserRestDayItem `json:"items"`
}

// DayOfWeekName 将星期数字转为中文名称
func DayOfWeekName(day int) string {
	names := []string{"", "周一", "周二", "周三", "周四", "周五", "周六", "周日"}
	if day >= 1 && day <= 7 {
		return names[day]
	}
	return ""
}
