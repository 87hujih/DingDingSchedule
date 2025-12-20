package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
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

// ---------------------------------------------------------
// 1. 数据结构
// ---------------------------------------------------------
type CourseDB struct {
	CourseName string `json:"course_name"`
	Teacher    string `json:"teacher"`
	Location   string `json:"location"`
	DayOfWeek  int    `json:"day_of_week"`
	Section    int    `json:"section"`
	WeekList   string `json:"week_list"`
}

// ---------------------------------------------------------
// 2. 主程序
// ---------------------------------------------------------
func main() {
	// 配置: 请将此处修改为你实际的文件路径
	sourceFile := "./aa.xls"
	tempXlsx := "./a.xlsx"

	// Step 1: 格式转换 (ETL)
	fmt.Println(">>> [Step 1] 正在检测并转换文件格式...")
	os.Remove(tempXlsx) // 清理旧文件

	if err := ConvertToXlsx(sourceFile, tempXlsx); err != nil {
		log.Fatalf("文件转换失败: %v", err)
	}
	defer func() {
		os.Remove(tempXlsx)
		fmt.Println("\n>>> [Clean] 临时文件已清理")
	}()

	// Step 2: 读取标准 Excel
	f, err := excelize.OpenFile(tempXlsx)
	if err != nil {
		log.Fatalf("无法打开转换后的文件: %v", err)
	}
	defer f.Close()

	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		log.Fatal(err)
	}

	var dbRecords []CourseDB

	fmt.Println(">>> [Step 2] 开始解析课程数据 (结构化行模式)...")

	// 动态表头识别
	colDayMap := make(map[int]int)
	headerFound := false
	for rIdx := 0; rIdx < 5 && rIdx < len(rows); rIdx++ {
		row := rows[rIdx]
		for cIdx, cell := range row {
			txt := strings.ReplaceAll(cell, " ", "")
			if strings.Contains(txt, "星期一") {
				colDayMap[cIdx] = 1
				headerFound = true
			}
			if strings.Contains(txt, "星期二") {
				colDayMap[cIdx] = 2
				headerFound = true
			}
			if strings.Contains(txt, "星期三") {
				colDayMap[cIdx] = 3
				headerFound = true
			}
			if strings.Contains(txt, "星期四") {
				colDayMap[cIdx] = 4
				headerFound = true
			}
			if strings.Contains(txt, "星期五") {
				colDayMap[cIdx] = 5
				headerFound = true
			}
			if strings.Contains(txt, "星期六") {
				colDayMap[cIdx] = 6
				headerFound = true
			}
			if strings.Contains(txt, "星期日") || strings.Contains(txt, "星期天") {
				colDayMap[cIdx] = 7
				headerFound = true
			}
		}
		if headerFound {
			break
		}
	}

	// Step 3: 遍历与解析
	for _, row := range rows {
		// 跳过表头行
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
					continue
				}
			} else {
				if colIdx < 2 {
					continue
				}
				dayOfWeek = colIdx - 1
				if dayOfWeek > 7 {
					dayOfWeek = 7
				}
			}

			cleanValue := strings.TrimSpace(cellValue)
			if cleanValue == "" {
				continue
			}

			var records []CourseDB

			// >>> 核心策略选择 <<<
			if strings.Contains(cleanValue, "\n") {
				// 策略 A: 完美结构解析 (基于4行结构)
				// 针对: 标准的 Excel 单元格内换行
				records = parseStructuredLines(cleanValue)
			} else {
				// 策略 B: 容错解析 (基于文本流)
				// 针对: 丢失了换行符的粘连文本
				normalized := normalizeAndPad(cleanValue)
				tokens := strings.Fields(normalized)
				records = parseGeneric(tokens)
			}

			for _, r := range records {
				r.DayOfWeek = dayOfWeek
				dbRecords = append(dbRecords, r)
			}
		}
	}

	// Step 4: 输出 SQL
	printSQL(dbRecords)
}

// ---------------------------------------------------------
// 3. 结构化解析 (针对标准 4 行模式) - NEW!
// ---------------------------------------------------------
func parseStructuredLines(cellValue string) []CourseDB {
	var records []CourseDB

	// 1. 切分行并清洗
	var lines []string
	rawLines := strings.Split(cellValue, "\n")
	for _, l := range rawLines {
		t := strings.TrimSpace(l)
		if t != "" {
			lines = append(lines, t)
		}
	}

	// 2. 遍历每一行
	for i, line := range lines {
		// 只有包含 [x-x] 格式的行才是时间锚点
		if isTimeAnchor(line) {

			// A. 解析时间 (Line i)
			weekPart, nodePart, weekType := parseTimeToken(line)

			// B. 解析老师 (Line i-1)
			teacher := "未知"
			if i-1 >= 0 {
				teacher = lines[i-1]
			}

			// C. 解析课程 (Line i-2)
			courseName := "未知课程"
			if i-2 >= 0 {
				courseName = lines[i-2]
			}

			// D. 解析地点 (Line i+1)
			location := "未定地点"
			if i+1 < len(lines) {
				// 只要下一行不是另一门课的时间锚点，就认为是地点
				if !isTimeAnchor(lines[i+1]) {
					location = lines[i+1]
				}
			}

			// E. 数据生成
			start, _ := parseNode(nodePart)
			weekInts := expandWeeksToArray(weekPart)
			finalWeeks := filterWeeks(weekInts, weekType)
			weekListStr := strings.Replace(fmt.Sprintf("%v", finalWeeks), " ", ",", -1)

			bigSection := 0
			if start > 0 {
				bigSection = (start + 1) / 2
			}

			records = append(records, CourseDB{
				CourseName: courseName,
				Teacher:    teacher,
				Location:   location, // 这里不需要再去猜测切分了，整行就是地点
				Section:    bigSection,
				WeekList:   weekListStr,
			})
		}
	}

	return records
}

// ---------------------------------------------------------
// 4. 通用解析 (兜底策略) - Backup
// ---------------------------------------------------------
func parseGeneric(tokens []string) []CourseDB {
	var records []CourseDB
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

		// 左侧
		currentLeftTokens := tokens[prevEnd:anchorIdx]
		currentLeftStr := strings.Join(currentLeftTokens, " ")
		fullHeaderStr := currentLeftStr
		if pendingStr != "" {
			fullHeaderStr = pendingStr + currentLeftStr
		}

		courseName, teacher := splitNameAndTeacherGeneric(fullHeaderStr)

		// 右侧
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
		weekListStr := strings.Replace(fmt.Sprintf("%v", finalWeeks), " ", ",", -1)
		bigSection := 0
		if start > 0 {
			bigSection = (start + 1) / 2
		}

		records = append(records, CourseDB{
			CourseName: courseName, Teacher: teacher, Location: realLocation,
			Section: bigSection, WeekList: weekListStr,
		})
		pendingStr = nextCourseStart
		prevEnd = locEnd
	}
	return records
}

// ---------------------------------------------------------
// 5. 辅助工具集
// ---------------------------------------------------------
func splitNameAndTeacherGeneric(raw string) (string, string) {
	if raw == "" {
		return "未知课程", "未知"
	}
	// 简单后空格切割
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
	// 保护实验室后缀
	protectedSuffixes := []string{"实验室", "中心", "机房", "基地", "平台", "实训室"}
	for _, suffix := range protectedSuffixes {
		if strings.HasSuffix(raw, suffix) {
			return raw, ""
		}
	}
	// 简单的正则边界
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

func printSQL(records []CourseDB) {
	fmt.Printf(">>> 解析完成！共提取 %d 条课程数据。\n", len(records))
	if len(records) == 0 {
		return
	}
	fmt.Println("INSERT INTO schedule (course_name, teacher, day, section, week_list, location) VALUES")
	for i, r := range records {
		suffix := ","
		if i == len(records)-1 {
			suffix = ";"
		}
		fmt.Printf("('%s', '%s', %d, %d, '%s', '%s')%s\n", r.CourseName, r.Teacher, r.DayOfWeek, r.Section, r.WeekList, r.Location, suffix)
	}
}

// ---------------------------------------------------------
// 6. 文件格式转换 (ETL)
// ---------------------------------------------------------
func ConvertToXlsx(srcPath, dstPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 1024)
	n, _ := f.Read(header)
	f.Seek(0, 0)
	validHeader := header[:n]
	if isXlsx(validHeader) {
		return copyFile(srcPath, dstPath)
	}
	if isBinaryXls(validHeader) {
		return convertBinaryXlsToXlsx(srcPath, dstPath)
	}
	if isHtml(validHeader) {
		return convertHtmlToXlsx(f, dstPath)
	}
	return fmt.Errorf("未知格式")
}

func convertHtmlToXlsx(reader io.Reader, dstPath string) error {
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
	f.SetSheetName("Sheet1", "Sheet1")
	rowIdx := 1
	doc.Find("tr").Each(func(i int, s *goquery.Selection) {
		colIdx := 1
		s.Find("td, th").Each(func(j int, cell *goquery.Selection) {
			// [重要修正] 将 <br> 替换为换行符 \n，保留结构！
			cell.Find("br").ReplaceWithHtml("\n")
			cell.Find("div").AppendHtml("\n")
			cell.Find("p").AppendHtml("\n")
			val := cell.Text()
			axis, _ := excelize.CoordinatesToCellName(colIdx, rowIdx)
			f.SetCellValue("Sheet1", axis, strings.TrimSpace(val))
			colIdx++
		})
		rowIdx++
	})
	return f.SaveAs(dstPath)
}

func isXlsx(header []byte) bool      { return bytes.HasPrefix(header, []byte{0x50, 0x4B, 0x03, 0x04}) }
func isBinaryXls(header []byte) bool { return bytes.HasPrefix(header, []byte{0xD0, 0xCF, 0x11, 0xE0}) }
func isHtml(header []byte) bool {
	s := strings.ToLower(string(header))
	return strings.Contains(s, "<html") || strings.Contains(s, "<table") || strings.Contains(s, "<!doctype html")
}
func copyFile(src, dst string) error {
	in, _ := os.Open(src)
	defer in.Close()
	out, _ := os.Create(dst)
	defer out.Close()
	io.Copy(out, in)
	return nil
}
func convertBinaryXlsToXlsx(srcPath, dstPath string) error {
	xlFile, _ := xls.Open(srcPath, "utf-8")
	f := excelize.NewFile()
	defer f.Close()
	if sheet1 := xlFile.GetSheet(0); sheet1 != nil {
		f.SetSheetName("Sheet1", "Sheet1")
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
