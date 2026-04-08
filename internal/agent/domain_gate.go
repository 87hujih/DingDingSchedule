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

// Hint 返回保守的领域提示，而不是最终裁决。
func (g *domainGate) Hint(question string) DomainHint {
	normalized := normalizeQuery(question)
	if normalized == "" {
		return domainHintObviousOut
	}

	obviousOutKeywords := []string{
		"天气",
		"气温",
		"新闻",
		"八卦",
		"股票",
		"基金",
		"比特币",
		"写代码",
		"写一个",
		"二分查找",
		"冒泡排序",
		"python",
		"java",
		"golang",
		"算法",
	}
	if containsAny(normalized, obviousOutKeywords) {
		return domainHintObviousOut
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
		return domainHintLikelyIn
	}
	return domainHintUnknown
}
