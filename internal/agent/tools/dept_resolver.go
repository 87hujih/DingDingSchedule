package tools

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

func resolveDeptFilter(ctx context.Context, dept DeptPort, deptID int64, deptName string) (resolvedID int64, useFilter bool, payload string, err error) {
	trimmed := strings.TrimSpace(deptName)
	if trimmed == "" {
		if deptID > 0 {
			return deptID, true, "", nil
		}
		return 0, false, "", nil
	}

	depts, err := dept.ListDepts(ctx)
	if err != nil {
		return 0, false, "", err
	}

	matches := findDeptMatchesByName(depts, trimmed)
	switch len(matches) {
	case 0:
		payload, err = marshalJSON(map[string]interface{}{
			"error": "未找到部门「" + trimmed + "」，请确认部门名称",
		})
		return 0, false, payload, err
	case 1:
		return matches[0].DeptID, true, "", nil
	default:
		payload, err = marshalJSON(map[string]interface{}{
			"error": "部门名称「" + trimmed + "」不唯一，请改用 dept_id",
		})
		return 0, false, payload, err
	}
}

var chineseNumberPattern = regexp.MustCompile(`[零〇一二两三四五六七八九十百千]+`)

func findDeptMatchesByName(depts []DeptItem, raw string) []DeptItem {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	exactMatches := make([]DeptItem, 0, 1)
	for _, item := range depts {
		if strings.TrimSpace(item.Name) == trimmed {
			exactMatches = append(exactMatches, item)
		}
	}
	if len(exactMatches) > 0 {
		return exactMatches
	}

	normalized := normalizeDeptName(trimmed)
	if normalized == "" {
		return nil
	}

	normalizedMatches := make([]DeptItem, 0, 1)
	for _, item := range depts {
		if normalizeDeptName(item.Name) == normalized {
			normalizedMatches = append(normalizedMatches, item)
		}
	}
	return normalizedMatches
}

func normalizeDeptName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}

	replacer := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\n", "",
		"\r", "",
		"（", "(",
		"）", ")",
	)
	normalized := strings.ToLower(replacer.Replace(trimmed))
	return chineseNumberPattern.ReplaceAllStringFunc(normalized, func(part string) string {
		value, ok := parseChineseNumber(part)
		if !ok {
			return part
		}
		return strconv.Itoa(value)
	})
}

func parseChineseNumber(value string) (int, bool) {
	if value == "" {
		return 0, false
	}

	digits := map[rune]int{
		'零': 0,
		'〇': 0,
		'一': 1,
		'二': 2,
		'两': 2,
		'三': 3,
		'四': 4,
		'五': 5,
		'六': 6,
		'七': 7,
		'八': 8,
		'九': 9,
	}
	units := map[rune]int{
		'十': 10,
		'百': 100,
		'千': 1000,
	}

	total := 0
	number := 0
	sawDigit := false
	for _, r := range value {
		if digit, ok := digits[r]; ok {
			number = digit
			sawDigit = true
			continue
		}
		unit, ok := units[r]
		if !ok {
			return 0, false
		}
		if number == 0 {
			number = 1
		}
		total += number * unit
		number = 0
		sawDigit = false
	}

	if !sawDigit && total == 0 {
		return 0, false
	}
	return total + number, true
}
