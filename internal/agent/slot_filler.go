package agent

import "sort"

type slotFillResult struct {
	Filled       map[string]string
	MatchedSlots []string
	Ready        bool
}

func fillTaskSlots(task *ActiveTask, reply string) slotFillResult {
	filled := make(map[string]string)
	if task != nil && task.FilledSlots != nil {
		for k, v := range task.FilledSlots {
			filled[k] = v
		}
	}

	normalized := normalizeQuery(reply)
	matched := make([]string, 0, 2)
	switch {
	case task == nil:
	case task.Type == "subscribe_attendance_push":
		fillSubscriptionSlots(filled, &matched, normalized, reply)
	case task.Type == "sign_for_user":
		fillManualSignSlots(filled, &matched, normalized, reply)
	}

	sort.Strings(matched)
	ready := task != nil && len(missingSlots(task.RequiredSlots, filled)) == 0
	return slotFillResult{
		Filled:       filled,
		MatchedSlots: matched,
		Ready:        ready,
	}
}

func fillSubscriptionSlots(filled map[string]string, matched *[]string, normalized, original string) {
	switch {
	case containsAny(normalized, []string{"全部人员", "全部"}):
		filled["scope"] = "all"
		recordMatchedSlot(matched, "scope")
	case containsAny(normalized, []string{"指定部门", "部分部门"}):
		filled["scope"] = "department"
		recordMatchedSlot(matched, "scope")
	default:
		if normalized != "" && !containsAny(normalized, []string{"订阅", "开启", "开通", "打开", "关闭", "取消", "推送", "考勤"}) {
			filled["scope"] = "department"
			filled["dept_names"] = original
			recordMatchedSlot(matched, "scope")
			recordMatchedSlot(matched, "dept_names")
		}
	}
}

func fillManualSignSlots(filled map[string]string, matched *[]string, normalized, original string) {
	switch {
	case containsAny(normalized, []string{"今天"}):
		filled["date"] = "today"
		recordMatchedSlot(matched, "date")
	case containsAny(normalized, []string{"昨天"}):
		filled["date"] = "yesterday"
		recordMatchedSlot(matched, "date")
	case containsAny(normalized, []string{"明天"}):
		filled["date"] = "tomorrow"
		recordMatchedSlot(matched, "date")
	}

	switch {
	case containsAny(normalized, []string{"第一节"}):
		filled["section"] = "1"
		recordMatchedSlot(matched, "section")
	case containsAny(normalized, []string{"第二节"}):
		filled["section"] = "2"
		recordMatchedSlot(matched, "section")
	case containsAny(normalized, []string{"第三节"}):
		filled["section"] = "3"
		recordMatchedSlot(matched, "section")
	case containsAny(normalized, []string{"第四节"}):
		filled["section"] = "4"
		recordMatchedSlot(matched, "section")
	case containsAny(normalized, []string{"第五节"}):
		filled["section"] = "5"
		recordMatchedSlot(matched, "section")
	}

	if original != "" &&
		!containsAny(normalized, []string{"今天", "昨天", "明天", "第", "节"}) &&
		!containsAny(normalized, []string{"补签", "代签"}) {
		filled["user_name"] = original
		recordMatchedSlot(matched, "user_name")
	}
}

func recordMatchedSlot(matched *[]string, name string) {
	if matched == nil || name == "" {
		return
	}
	for _, existing := range *matched {
		if existing == name {
			return
		}
	}
	*matched = append(*matched, name)
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
