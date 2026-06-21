package agent

import "sort"

type slotFillResult struct {
	Filled       map[string]string
	MatchedSlots []string
	Ready        bool
}

// fillTaskSlots handles fill task slots.
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

// fillSubscriptionSlots handles fill subscription slots.
func fillSubscriptionSlots(filled map[string]string, matched *[]string, normalized, _ string) {
	switch {
	case containsAny(normalized, []string{"全部人员", "全部"}):
		filled["scope"] = "all"
		recordMatchedSlot(matched, "scope")
	case containsAny(normalized, []string{"指定部门", "部分部门"}):
		filled["scope"] = "department"
		recordMatchedSlot(matched, "scope")
	}
}

// fillManualSignSlots handles fill manual sign slots.
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

// recordMatchedSlot records matched slot.
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

// missingSlots returns missing slots.
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

// isDepartmentListQuestion reports whether it is department list question.
func isDepartmentListQuestion(question string) bool {
	return containsAny(question, []string{
		"都有哪些部门",
		"有哪些部门",
		"有什么部门",
		"列出部门",
		"部门列表",
		"可选部门",
	})
}
