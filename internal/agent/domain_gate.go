package agent

type domainResult string

const (
	domainIn  domainResult = "in_domain"
	domainOut domainResult = "out_of_domain"
)

type domainGate struct{}

// newDomainGate 创建首版领域门禁，只负责站内/站外判定。
func newDomainGate() *domainGate {
	return &domainGate{}
}

// Check 判断问题是否属于课表、考勤、请假、作息或系统说明等站内范围。
func (g *domainGate) Check(question string) domainResult {
	normalized := normalizeQuery(question)
	if normalized == "" {
		return domainOut
	}

	keywords := []string{
		"课表",
		"课程",
		"考勤",
		"请假",
		"作息",
		"节次",
		"迟到",
		"缺勤",
		"出勤",
		"未到",
		"无课",
		"没课",
		"签到",
		"补签",
		"休息日",
		"实时视图",
		"最终结算",
		"后台",
		"知识库",
		"功能",
		"系统",
	}
	if containsAny(normalized, keywords) {
		return domainIn
	}
	return domainOut
}
