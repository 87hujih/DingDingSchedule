package agent

type slotFillResult struct {
	Filled map[string]string
	Ready  bool
}

func fillTaskSlots(task *ActiveTask, reply string) slotFillResult {
	filled := make(map[string]string)
	if task != nil && task.FilledSlots != nil {
		for k, v := range task.FilledSlots {
			filled[k] = v
		}
	}

	normalized := normalizeQuery(reply)
	switch {
	case task == nil:
	case task.Type == "subscribe_attendance_push":
		fillSubscriptionSlots(filled, normalized, reply)
	case task.Type == "sign_for_user":
		fillManualSignSlots(filled, normalized, reply)
	}

	ready := task != nil && len(missingSlots(task.RequiredSlots, filled)) == 0
	return slotFillResult{
		Filled: filled,
		Ready:  ready,
	}
}

func fillSubscriptionSlots(filled map[string]string, normalized, original string) {
	switch {
	case containsAny(normalized, []string{"全部人员", "全部"}):
		filled["scope"] = "all"
	case containsAny(normalized, []string{"指定部门", "部分部门"}):
		filled["scope"] = "department"
	default:
		if normalized != "" && !containsAny(normalized, []string{"订阅", "开启", "开通", "打开", "关闭", "取消", "推送", "考勤"}) {
			filled["scope"] = "department"
			filled["dept_names"] = original
		}
	}
}

func fillManualSignSlots(filled map[string]string, normalized, original string) {
	switch {
	case containsAny(normalized, []string{"今天"}):
		filled["date"] = "today"
	case containsAny(normalized, []string{"昨天"}):
		filled["date"] = "yesterday"
	case containsAny(normalized, []string{"明天"}):
		filled["date"] = "tomorrow"
	}

	switch {
	case containsAny(normalized, []string{"第一节"}):
		filled["section"] = "1"
	case containsAny(normalized, []string{"第二节"}):
		filled["section"] = "2"
	case containsAny(normalized, []string{"第三节"}):
		filled["section"] = "3"
	case containsAny(normalized, []string{"第四节"}):
		filled["section"] = "4"
	case containsAny(normalized, []string{"第五节"}):
		filled["section"] = "5"
	}

	if original != "" &&
		!containsAny(normalized, []string{"今天", "昨天", "明天", "第", "节"}) &&
		!containsAny(normalized, []string{"补签", "代签"}) {
		filled["user_name"] = original
	}
}

func missingSlots(required []string, filled map[string]string) []string {
	missing := make([]string, 0, len(required))
	for _, slot := range required {
		if _, ok := filled[slot]; ok {
			continue
		}
		missing = append(missing, slot)
	}
	return missing
}
