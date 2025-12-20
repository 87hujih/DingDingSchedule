package scheduleparse

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"github.com/extrame/xls"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
)

// CourseInput 解析后的课程数据
type CourseInput struct {
	CourseName string
	Teacher    string
	Location   string
	DayOfWeek  int
	Section    int
	WeekList   string
}

// ConvertToXLSX 将多种来源转换为标准 xlsx 文件（输出 dstPath）
func ConvertToXLSX(ctx context.Context, srcPath, dstPath string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 1024)
	n, _ := f.Read(header)
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	validHeader := header[:n]

	switch {
	case isXLSX(validHeader):
		return copyFile(srcPath, dstPath)
	case isBinaryXLS(validHeader):
		return convertBinaryXLSToXLSX(f, dstPath)
	case isHTML(validHeader):
		// 这里调用修复后的 HTML 转换函数
		return convertHTMLToXLSX(f, dstPath)
	default:
		return fmt.Errorf("unsupported file format")
	}
}

// ParseCourses 读取标准 xlsx 并解析为课程列表
func ParseCourses(ctx context.Context, xlsxPath string) ([]CourseInput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	f, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, err
	}

	colDayMap := make(map[int]int)
	headerFound := false

	// 尝试在前几行寻找表头
	for rIdx := 0; rIdx < 10 && rIdx < len(rows); rIdx++ {
		row := rows[rIdx]
		// 统计这一行包含"星期"的数量，避免误判
		weekCount := 0
		tempMap := make(map[int]int)

		for cIdx, cell := range row {
			txt := strings.ReplaceAll(cell, " ", "")
			if strings.Contains(txt, "星期") {
				weekCount++
				if strings.Contains(txt, "一") {
					tempMap[cIdx] = 1
				}
				if strings.Contains(txt, "二") {
					tempMap[cIdx] = 2
				}
				if strings.Contains(txt, "三") {
					tempMap[cIdx] = 3
				}
				if strings.Contains(txt, "四") {
					tempMap[cIdx] = 4
				}
				if strings.Contains(txt, "五") {
					tempMap[cIdx] = 5
				}
				if strings.Contains(txt, "六") {
					tempMap[cIdx] = 6
				}
				if strings.Contains(txt, "日") || strings.Contains(txt, "天") {
					tempMap[cIdx] = 7
				}
			}
		}

		// 如果一行里至少有3个“星期X”，我们认为这是表头行
		if weekCount >= 3 {
			colDayMap = tempMap
			headerFound = true
			break
		}
	}

	var result []CourseInput
	for _, row := range rows {
		// 跳过纯表头行
		isHeader := false
		for _, cell := range row {
			if strings.Contains(cell, "星期") {
				isHeader = true
				break
			}
		}
		if isHeader {
			continue
		}

		for colIdx, cellValue := range row {
			dayOfWeek := 0

			if headerFound {
				if d, ok := colDayMap[colIdx]; ok {
					dayOfWeek = d
				} else {
					// 如果找到了表头，但这列不在映射里（比如时间列），跳过
					continue
				}
			} else {
				// 兜底逻辑：通常前2列是时间/节次，第3列开始是星期一
				// 如果 HTML 转换修复了，这里的列索引应该是准确的
				if colIdx < 2 {
					continue
				}
				dayOfWeek = colIdx - 1 // 假设 col 2 -> Mon (1)
				if dayOfWeek > 7 {
					dayOfWeek = 7
				}
			}

			cleanValue := strings.TrimSpace(cellValue)
			if cleanValue == "" {
				continue
			}

			// 解析单元格内容
			var records []CourseInput
			if strings.Contains(cleanValue, "\n") {
				records = parseStructuredLines(cleanValue)
			} else {
				normalized := normalizeAndPad(cleanValue)
				tokens := strings.Fields(normalized)
				records = parseGeneric(tokens)
			}

			for _, r := range records {
				r.DayOfWeek = dayOfWeek
				result = append(result, r)
			}
		}
	}

	return result, nil
}

// ---------- 核心修复：HTML 转 XLSX (支持跨行/跨列) ----------

func convertHTMLToXLSX(reader io.Reader, dstPath string) error {
	bufferedReader := bufio.NewReader(reader)
	sample, _ := bufferedReader.Peek(1024)
	e, _, _ := charset.DetermineEncoding(sample, "text/html")
	utf8Reader := transform.NewReader(bufferedReader, e.NewDecoder())
	doc, err := goquery.NewDocumentFromReader(utf8Reader)
	if err != nil {
		return err
	}

	f := excelize.NewFile()
	defer f.Close()
	sheetName := "Sheet1"
	f.SetSheetName("Sheet1", sheetName)

	// occupied 用于记录被 rowspan/colspan 占用的格子
	// map[row_index]map[col_index]bool
	occupied := make(map[int]map[int]bool)

	// 辅助函数：标记占用
	markOccupied := func(r, c int) {
		if occupied[r] == nil {
			occupied[r] = make(map[int]bool)
		}
		occupied[r][c] = true
	}

	// 辅助函数：检查是否被占用
	isOccupied := func(r, c int) bool {
		if rowMap, ok := occupied[r]; ok {
			return rowMap[c]
		}
		return false
	}

	rowIdx := 1
	doc.Find("tr").Each(func(i int, tr *goquery.Selection) {
		colIdx := 1
		tr.Find("td, th").Each(func(j int, cell *goquery.Selection) {
			// 1. 如果当前格子已经被上一行的 rowspan 占用，跳过这些列
			for isOccupied(rowIdx, colIdx) {
				colIdx++
			}

			// 2. 获取跨行跨列属性
			rowSpan, _ := strconv.Atoi(cell.AttrOr("rowspan", "1"))
			colSpan, _ := strconv.Atoi(cell.AttrOr("colspan", "1"))
			if rowSpan < 1 {
				rowSpan = 1
			}
			if colSpan < 1 {
				colSpan = 1
			}

			// 3. 处理文本内容
			cell.Find("br").ReplaceWithHtml("\n")
			cell.Find("div").AppendHtml("\n")
			cell.Find("p").AppendHtml("\n")
			val := strings.TrimSpace(cell.Text())

			// 4. 写入 Excel (只写入左上角的那个单元格)
			axis, _ := excelize.CoordinatesToCellName(colIdx, rowIdx)
			f.SetCellValue(sheetName, axis, val)

			// 5. 将当前单元格覆盖的所有区域标记为占用
			// 注意：这包括了当前格子本身以及未来行/列的格子
			for r := 0; r < rowSpan; r++ {
				for c := 0; c < colSpan; c++ {
					markOccupied(rowIdx+r, colIdx+c)
				}
			}

			// 6. 移动列指针
			colIdx += colSpan
		})
		rowIdx++
	})

	return f.SaveAs(dstPath)
}

// ---------- 结构化解析 ----------

func parseStructuredLines(cellValue string) []CourseInput {
	var records []CourseInput

	var lines []string
	for _, l := range strings.Split(cellValue, "\n") {
		t := strings.TrimSpace(l)
		if t != "" {
			lines = append(lines, t)
		}
	}

	for i, line := range lines {
		if !isTimeAnchor(line) {
			continue
		}

		weekPart, nodePart, weekType := parseTimeToken(line)
		teacher := "未知"
		if i-1 >= 0 {
			teacher = lines[i-1]
		}
		courseName := "未知课程"
		if i-2 >= 0 {
			courseName = lines[i-2]
		}
		location := "未定地点"
		if i+1 < len(lines) && !isTimeAnchor(lines[i+1]) {
			location = lines[i+1]
		}

		start, _ := parseNode(nodePart)
		weekInts := expandWeeksToArray(weekPart)
		finalWeeks := filterWeeks(weekInts, weekType)
		weekListStr := joinWeeks(finalWeeks)

		bigSection := 0
		if start > 0 {
			bigSection = (start + 1) / 2
		}

		records = append(records, CourseInput{
			CourseName: courseName,
			Teacher:    teacher,
			Location:   location,
			Section:    bigSection,
			WeekList:   weekListStr,
		})
	}
	return records
}

// ---------- 容错解析 ----------

func parseGeneric(tokens []string) []CourseInput {
	var records []CourseInput
	var anchorIndices []int
	for i, token := range tokens {
		if isTimeAnchor(token) {
			anchorIndices = append(anchorIndices, i)
		}
	}
	if len(anchorIndices) == 0 {
		return nil
	}

	pendingStr := ""
	prevEnd := 0
	for i, anchorIdx := range anchorIndices {
		rawTime := tokens[anchorIdx]
		weekPart, nodePart, weekType := parseTimeToken(rawTime)

		currentLeftTokens := tokens[prevEnd:anchorIdx]
		currentLeftStr := strings.Join(currentLeftTokens, " ")
		fullHeaderStr := currentLeftStr
		if pendingStr != "" {
			fullHeaderStr = pendingStr + currentLeftStr
		}
		courseName, teacher := splitNameAndTeacherGeneric(fullHeaderStr)

		locStart := anchorIdx + 1
		locEnd := len(tokens)
		if i+1 < len(anchorIndices) {
			locEnd = anchorIndices[i+1]
		}
		rawRightStr := ""
		if locStart < locEnd {
			rawRightStr = strings.Join(tokens[locStart:locEnd], "")
		}
		realLocation, nextCourseStart := splitLocationAndNextCourse(rawRightStr)

		start, _ := parseNode(nodePart)
		weekInts := expandWeeksToArray(weekPart)
		finalWeeks := filterWeeks(weekInts, weekType)
		weekListStr := joinWeeks(finalWeeks)
		bigSection := 0
		if start > 0 {
			bigSection = (start + 1) / 2
		}

		records = append(records, CourseInput{
			CourseName: courseName,
			Teacher:    teacher,
			Location:   realLocation,
			Section:    bigSection,
			WeekList:   weekListStr,
		})
		pendingStr = nextCourseStart
		prevEnd = locEnd
	}
	return records
}

// ---------- 公共工具 ----------

func splitNameAndTeacherGeneric(raw string) (string, string) {
	if raw == "" {
		return "未知课程", "未知"
	}
	if idx := strings.LastIndex(raw, " "); idx != -1 {
		t := raw[idx+1:]
		if !containsCourseKeyword(t) && len(t) < 15 {
			return strings.TrimSpace(raw[:idx]), t
		}
	}
	return raw, "未知"
}

func splitLocationAndNextCourse(raw string) (string, string) {
	if raw == "" {
		return "", ""
	}
	protectedSuffixes := []string{"实验室", "中心", "机房", "基地", "平台", "实训室", "楼", "区", "室"}
	for _, suffix := range protectedSuffixes {
		if strings.HasSuffix(raw, suffix) {
			return raw, ""
		}
	}
	reBoundary := regexp.MustCompile(`([A-Za-z0-9#]+)([\p{Han}])`)
	locs := reBoundary.FindStringSubmatchIndex(raw)
	if locs != nil {
		splitIdx := locs[3]
		rightPart := raw[splitIdx:]
		isSafe := false
		for _, s := range protectedSuffixes {
			if strings.Contains(rightPart, s) {
				isSafe = true
				break
			}
		}
		if !isSafe {
			return raw[:splitIdx], rightPart
		}
	}
	return raw, ""
}

func normalizeAndPad(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) || r == '\u3000' || r == '\u2002' {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	s = b.String()
	reAnchor := regexp.MustCompile(`([0-9,\-周单双]+)(\[|【)([0-9,\-]+)(\]|】)`)
	s = reAnchor.ReplaceAllString(s, " $0 ")
	return regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
}

func isTimeAnchor(s string) bool {
	return strings.ContainsAny(s, "[]【】") && (strings.ContainsAny(s, "0123456789") || strings.Contains(s, "单") || strings.Contains(s, "双"))
}

func parseTimeToken(token string) (string, string, int) {
	reNode := regexp.MustCompile(`(\[|【)(.*?)(\]|】)`)
	matches := reNode.FindStringSubmatch(token)
	nodePart := ""
	if len(matches) >= 3 {
		nodePart = matches[2]
	}
	weekPartRaw := reNode.ReplaceAllString(token, "")
	weekType := 0
	if strings.Contains(weekPartRaw, "单") {
		weekType = 1
	} else if strings.Contains(weekPartRaw, "双") {
		weekType = 2
	}
	reClean := regexp.MustCompile(`[^0-9,\-]`)
	weekPartRaw = reClean.ReplaceAllString(weekPartRaw, "")
	return weekPartRaw, nodePart, weekType
}

func parseNode(raw string) (int, int) {
	parts := strings.Split(raw, "-")
	start, _ := strconv.Atoi(parts[0])
	end := start
	if len(parts) > 1 {
		end, _ = strconv.Atoi(parts[1])
	}
	return start, end
}

func expandWeeksToArray(raw string) []int {
	var weeks []int
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		if strings.Contains(p, "-") {
			rp := strings.Split(p, "-")
			if len(rp) == 2 {
				s, _ := strconv.Atoi(rp[0])
				e, _ := strconv.Atoi(rp[1])
				for i := s; i <= e; i++ {
					weeks = append(weeks, i)
				}
			}
		} else {
			i, _ := strconv.Atoi(p)
			if i > 0 {
				weeks = append(weeks, i)
			}
		}
	}
	sort.Ints(weeks)
	return weeks
}

func filterWeeks(weeks []int, weekType int) []int {
	if weekType == 0 {
		return weeks
	}
	var filtered []int
	for _, w := range weeks {
		if (weekType == 1 && w%2 != 0) || (weekType == 2 && w%2 == 0) {
			filtered = append(filtered, w)
		}
	}
	return filtered
}

func containsCourseKeyword(s string) bool {
	return strings.ContainsAny(s, "课论学基组室楼区程")
}

func joinWeeks(weeks []int) string {
	if len(weeks) == 0 {
		return ""
	}
	var b strings.Builder
	for i, w := range weeks {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(strconv.Itoa(w))
	}
	return b.String()
}

// ---------- 文件转换基础 ----------

func isXLSX(header []byte) bool      { return bytes.HasPrefix(header, []byte{0x50, 0x4B, 0x03, 0x04}) }
func isBinaryXLS(header []byte) bool { return bytes.HasPrefix(header, []byte{0xD0, 0xCF, 0x11, 0xE0}) }
func isHTML(header []byte) bool {
	s := strings.ToLower(string(header))
	return strings.Contains(s, "<html") || strings.Contains(s, "<table") || strings.Contains(s, "<!doctype html")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func convertBinaryXLSToXLSX(reader io.Reader, dstPath string) error {
	tmpFile, err := os.CreateTemp("", "binary-xls-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	if _, err = io.Copy(tmpFile, reader); err != nil {
		return err
	}
	if _, err = tmpFile.Seek(0, 0); err != nil {
		return err
	}

	xlFile, err := xls.Open(tmpFile.Name(), "utf-8")
	if err != nil {
		return err
	}

	f := excelize.NewFile()
	defer f.Close()
	f.SetSheetName("Sheet1", "Sheet1")

	if sheet1 := xlFile.GetSheet(0); sheet1 != nil {
		for i := 0; i <= int(sheet1.MaxRow); i++ {
			row := sheet1.Row(i)
			if row == nil {
				continue
			}
			for j := 0; j <= int(row.LastCol()); j++ {
				axis, _ := excelize.CoordinatesToCellName(j+1, i+1)
				f.SetCellValue("Sheet1", axis, row.Col(j))
			}
		}
	}
	return f.SaveAs(dstPath)
}
