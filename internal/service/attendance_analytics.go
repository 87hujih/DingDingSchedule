package service

// attendance_analytics.go
// 为 AttendanceRecordService 扩展两个 Agent 查询方法：
//   - QueryStats      实现 agent.AttendanceStatsPort
//   - QueryUserCross  实现 agent.UserCrossPort

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	agenttool "schedule_server/internal/agent/tools"
	"schedule_server/internal/model"
	"schedule_server/pkg/weekutil"
)

// ─────────────────────────────────────────────────────────────────────────────
// QueryStats — 考勤统计（按用户/部门/周次/节次聚合）
// ─────────────────────────────────────────────────────────────────────────────

func (s *AttendanceRecordService) QueryStats(
	ctx context.Context,
	req agenttool.AttendanceStatsQuery,
) ([]agenttool.AttendanceStatItem, error) {
	// 1. 解析时间范围
	startDate, endDate, err := s.resolveStatsDateRange(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("解析时间范围失败: %w", err)
	}

	// 2. 查考勤记录
	records, err := s.attendanceRecordRepo.ListByDateRange(ctx, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("查询考勤记录失败: %w", err)
	}

	// 3. 节次过滤
	records = filterStatRecordsBySection(records, req.Section, req.Sections)
	if len(records) == 0 {
		return []agenttool.AttendanceStatItem{}, nil
	}

	// 4. 加载用户（参与考勤 + 可选姓名/部门过滤）
	users, err := s.loadStatsUsers(ctx, req.UserName, req.DeptID)
	if err != nil {
		return nil, fmt.Errorf("查询用户信息失败: %w", err)
	}

	allowedIDs := make(map[uint]struct{}, len(users))
	userNameMap := make(map[uint]string, len(users))
	for _, u := range users {
		allowedIDs[u.ID] = struct{}{}
		userNameMap[u.ID] = u.Name
	}

	// 5. 聚合
	items, err := s.dispatchStatsGroupBy(ctx, records, allowedIDs, userNameMap, req.GroupBy)
	if err != nil {
		return nil, err
	}

	// 6. HAVING 过滤
	items = applyStatHaving(items, req)

	// 7. 排序
	sortStatItems(items, req.SortBy, req.SortOrder)

	// 8. Limit
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(items) > limit {
		items = items[:limit]
	}

	return items, nil
}

// resolveStatsDateRange 将各种时间参数统一解析为 [startDate, endDate)
// 优先级：DateRange > Date > WeekRange > Week > 默认（今天）
func (s *AttendanceRecordService) resolveStatsDateRange(
	ctx context.Context,
	req agenttool.AttendanceStatsQuery,
) (time.Time, time.Time, error) {
	if req.DateRange[0] != "" && req.DateRange[1] != "" {
		start, e1 := time.ParseInLocation("2006-01-02", req.DateRange[0], time.Local)
		end, e2 := time.ParseInLocation("2006-01-02", req.DateRange[1], time.Local)
		if e1 == nil && e2 == nil {
			return start, end.AddDate(0, 0, 1), nil
		}
	}

	if req.Date != "" {
		d, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
		if err == nil {
			return d, d.AddDate(0, 0, 1), nil
		}
	}

	if s.semesterSrv != nil {
		if req.WeekRange[0] > 0 && req.WeekRange[1] > 0 {
			start, _, err := s.semesterSrv.GetWeekDateRange(ctx, req.WeekRange[0])
			if err != nil {
				return time.Time{}, time.Time{}, err
			}
			_, end, err := s.semesterSrv.GetWeekDateRange(ctx, req.WeekRange[1])
			if err != nil {
				return time.Time{}, time.Time{}, err
			}
			return start, end, nil
		}
		if req.Week > 0 {
			return s.semesterSrv.GetWeekDateRange(ctx, req.Week)
		}
	}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return today, today.AddDate(0, 0, 1), nil
}

// loadStatsUsers 加载参与考勤的用户，按姓名/部门可选过滤
func (s *AttendanceRecordService) loadStatsUsers(
	ctx context.Context,
	userName string,
	deptID int64,
) ([]model.User, error) {
	var deptIDs []int64
	if deptID > 0 {
		deptIDs = []int64{deptID}
	}
	users, err := s.userRepo.ListAttendanceCandidates(ctx, deptIDs)
	if err != nil {
		return nil, err
	}

	active := make([]model.User, 0, len(users))
	for _, u := range users {
		if userName != "" && !strings.Contains(u.Name, userName) {
			continue
		}
		active = append(active, u)
	}
	return active, nil
}

// filterStatRecordsBySection 节次白名单过滤（都为零则不过滤）
func filterStatRecordsBySection(records []model.AttendanceRecord, section int, sections []int) []model.AttendanceRecord {
	if section == 0 && len(sections) == 0 {
		return records
	}
	allow := make(map[int]struct{})
	if section > 0 {
		allow[section] = struct{}{}
	}
	for _, s := range sections {
		allow[s] = struct{}{}
	}
	out := make([]model.AttendanceRecord, 0, len(records))
	for _, r := range records {
		if _, ok := allow[r.Section]; ok {
			out = append(out, r)
		}
	}
	return out
}

// dispatchStatsGroupBy 按 groupBy 分发到不同聚合实现
func (s *AttendanceRecordService) dispatchStatsGroupBy(
	ctx context.Context,
	records []model.AttendanceRecord,
	allowedIDs map[uint]struct{},
	userNameMap map[uint]string,
	groupBy string,
) ([]agenttool.AttendanceStatItem, error) {
	switch groupBy {
	case "user":
		return aggregateStatsByUser(records, allowedIDs, userNameMap), nil
	case "dept":
		userIDs := make([]uint, 0, len(allowedIDs))
		for uid := range allowedIDs {
			userIDs = append(userIDs, uid)
		}
		deptNameMap, err := s.userRepo.GetUserDepartmentNames(ctx, userIDs)
		if err != nil {
			return nil, fmt.Errorf("查询部门信息失败: %w", err)
		}
		return aggregateStatsByDept(records, allowedIDs, deptNameMap), nil
	case "week":
		return aggregateStatsByWeek(records, allowedIDs), nil
	case "section":
		return aggregateStatsBySection(records, allowedIDs), nil
	case "day":
		return aggregateStatsByDay(records, allowedIDs), nil
	default:
		return aggregateStatsPerRecord(records, allowedIDs), nil
	}
}

// ─── 各聚合函数 ───────────────────────────────────────────────────────────────

type statsAccum struct {
	onTime int
	leave  int
	absent int
}

func (a *statsAccum) total() int { return a.onTime + a.leave + a.absent }

func accumToStatItem(label string, acc *statsAccum) agenttool.AttendanceStatItem {
	total := acc.total()
	return agenttool.AttendanceStatItem{
		Label:       label,
		OnTimeCount: acc.onTime,
		LeaveCount:  acc.leave,
		AbsentCount: acc.absent,
		TotalCount:  total,
		OnTimeRate:  formatAttendRate(acc.onTime, total),
	}
}

func collectStatsAccum(
	accum map[string]*statsAccum,
	record model.AttendanceRecord,
	allowedIDs map[uint]struct{},
	keyFn func(uid uint) string,
) {
	add := func(idsJSON string, incFn func(*statsAccum)) {
		for _, uid := range parseAttendIDs(idsJSON) {
			if len(allowedIDs) > 0 {
				if _, ok := allowedIDs[uid]; !ok {
					continue
				}
			}
			key := keyFn(uid)
			if key == "" {
				continue
			}
			if _, ok := accum[key]; !ok {
				accum[key] = &statsAccum{}
			}
			incFn(accum[key])
		}
	}
	add(record.OnTimeIDs, func(a *statsAccum) { a.onTime++ })
	add(record.LeaveIDs, func(a *statsAccum) { a.leave++ })
	add(record.NotArrivedIDs, func(a *statsAccum) { a.absent++ })
}

func aggregateStatsByUser(
	records []model.AttendanceRecord,
	allowedIDs map[uint]struct{},
	userNameMap map[uint]string,
) []agenttool.AttendanceStatItem {
	accum := make(map[string]*statsAccum)
	for _, r := range records {
		collectStatsAccum(accum, r, allowedIDs, func(uid uint) string {
			return userNameMap[uid] // "" 若不在 map 中会被跳过
		})
	}
	items := make([]agenttool.AttendanceStatItem, 0, len(accum))
	for label, acc := range accum {
		items = append(items, accumToStatItem(label, acc))
	}
	return items
}

func aggregateStatsByDept(
	records []model.AttendanceRecord,
	allowedIDs map[uint]struct{},
	deptNameMap map[uint]string,
) []agenttool.AttendanceStatItem {
	accum := make(map[string]*statsAccum)
	for _, r := range records {
		collectStatsAccum(accum, r, allowedIDs, func(uid uint) string {
			name := deptNameMap[uid]
			if name == "" {
				return "未知部门"
			}
			return name
		})
	}
	items := make([]agenttool.AttendanceStatItem, 0, len(accum))
	for label, acc := range accum {
		items = append(items, accumToStatItem(label, acc))
	}
	return items
}

func aggregateStatsByWeek(
	records []model.AttendanceRecord,
	allowedIDs map[uint]struct{},
) []agenttool.AttendanceStatItem {
	accum := make(map[string]*statsAccum)
	for _, r := range records {
		label := fmt.Sprintf("第%d周", r.Week)
		collectStatsAccum(accum, r, allowedIDs, func(_ uint) string { return label })
	}
	items := make([]agenttool.AttendanceStatItem, 0, len(accum))
	for label, acc := range accum {
		items = append(items, accumToStatItem(label, acc))
	}
	return items
}

func aggregateStatsBySection(
	records []model.AttendanceRecord,
	allowedIDs map[uint]struct{},
) []agenttool.AttendanceStatItem {
	accum := make(map[string]*statsAccum)
	for _, r := range records {
		label := fmt.Sprintf("第%d节", r.Section)
		collectStatsAccum(accum, r, allowedIDs, func(_ uint) string { return label })
	}
	items := make([]agenttool.AttendanceStatItem, 0, len(accum))
	for label, acc := range accum {
		items = append(items, accumToStatItem(label, acc))
	}
	return items
}

var weekdayNames = [8]string{"", "周一", "周二", "周三", "周四", "周五", "周六", "周日"}

func aggregateStatsByDay(
	records []model.AttendanceRecord,
	allowedIDs map[uint]struct{},
) []agenttool.AttendanceStatItem {
	accum := make(map[string]*statsAccum)
	for _, r := range records {
		wd := int(r.Date.Weekday()) // 0=Sunday
		// 转为 1=Monday … 7=Sunday
		mon1 := wd - 1
		if mon1 < 0 {
			mon1 = 6
		}
		mon1++ // 1-based
		label := weekdayNames[mon1]
		collectStatsAccum(accum, r, allowedIDs, func(_ uint) string { return label })
	}
	items := make([]agenttool.AttendanceStatItem, 0, len(accum))
	for label, acc := range accum {
		items = append(items, accumToStatItem(label, acc))
	}
	return items
}

func aggregateStatsPerRecord(
	records []model.AttendanceRecord,
	allowedIDs map[uint]struct{},
) []agenttool.AttendanceStatItem {
	items := make([]agenttool.AttendanceStatItem, 0, len(records))
	for _, r := range records {
		label := fmt.Sprintf("%s 第%d节", r.Date.Format("2006-01-02"), r.Section)
		accum := map[string]*statsAccum{label: {}}
		collectStatsAccum(accum, r, allowedIDs, func(_ uint) string { return label })
		items = append(items, accumToStatItem(label, accum[label]))
	}
	return items
}

// applyStatHaving 聚合后 HAVING 过滤
func applyStatHaving(items []agenttool.AttendanceStatItem, req agenttool.AttendanceStatsQuery) []agenttool.AttendanceStatItem {
	if req.MinAbsentCount <= 0 && req.MaxOnTimeRate <= 0 {
		return items
	}
	out := make([]agenttool.AttendanceStatItem, 0, len(items))
	for _, item := range items {
		if req.MinAbsentCount > 0 && item.AbsentCount < req.MinAbsentCount {
			continue
		}
		if req.MaxOnTimeRate > 0 {
			rate := parseRateFloat(item.OnTimeRate)
			if rate > req.MaxOnTimeRate {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

// sortStatItems 按指定字段排序
func sortStatItems(items []agenttool.AttendanceStatItem, sortBy, sortOrder string) {
	if len(items) == 0 {
		return
	}
	desc := sortOrder != "asc"
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		var va, vb float64
		switch sortBy {
		case "on_time_count":
			va, vb = float64(a.OnTimeCount), float64(b.OnTimeCount)
		case "on_time_rate":
			va, vb = parseRateFloat(a.OnTimeRate), parseRateFloat(b.OnTimeRate)
		case "leave_count":
			va, vb = float64(a.LeaveCount), float64(b.LeaveCount)
		default: // "absent_count"
			va, vb = float64(a.AbsentCount), float64(b.AbsentCount)
		}
		if desc {
			return va > vb
		}
		return va < vb
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// QueryUserCross — 人员交叉查询
// ─────────────────────────────────────────────────────────────────────────────

func (s *AttendanceRecordService) QueryUserCross(
	ctx context.Context,
	req agenttool.UserCrossQuery,
) ([]string, error) {
	// 1. 加载候选用户
	candidates, err := s.loadCrossUsers(ctx, req.DeptID, req.UserNames)
	if err != nil {
		return nil, fmt.Errorf("查询候选用户失败: %w", err)
	}
	if len(candidates) == 0 {
		return []string{}, nil
	}

	userIDs := make([]uint, 0, len(candidates))
	for _, u := range candidates {
		userIDs = append(userIDs, u.ID)
	}

	// candidateSet 用于快速查找和逐步缩减
	candidateSet := make(map[uint]struct{}, len(candidates))
	for _, u := range candidates {
		candidateSet[u.ID] = struct{}{}
	}

	// 2. free_slots：移除有课的用户（AND 语义）
	for _, slot := range req.FreeSlots {
		if len(candidateSet) == 0 {
			break
		}
		busySet, err := s.getBusyUserSet(ctx, userIDs, slot)
		if err != nil {
			return nil, fmt.Errorf("查询有课用户失败: %w", err)
		}
		for uid := range busySet {
			delete(candidateSet, uid)
		}
	}

	// 3. busy_slots：只保留有课的用户（AND 语义）
	for _, slot := range req.BusySlots {
		if len(candidateSet) == 0 {
			break
		}
		busySet, err := s.getBusyUserSet(ctx, userIDs, slot)
		if err != nil {
			return nil, fmt.Errorf("查询有课用户失败: %w", err)
		}
		newSet := make(map[uint]struct{})
		for uid := range busySet {
			if _, ok := candidateSet[uid]; ok {
				newSet[uid] = struct{}{}
			}
		}
		candidateSet = newSet
	}

	// 4. absent_on：只保留曾缺勤的用户（OR 语义）
	if len(req.AbsentOn) > 0 && len(candidateSet) > 0 {
		absentSet, err := s.getAbsentUserSet(ctx, req.AbsentOn)
		if err != nil {
			return nil, fmt.Errorf("查询缺勤用户失败: %w", err)
		}
		newSet := make(map[uint]struct{})
		for uid := range absentSet {
			if _, ok := candidateSet[uid]; ok {
				newSet[uid] = struct{}{}
			}
		}
		candidateSet = newSet
	}

	// 5. 按原始顺序输出姓名
	names := make([]string, 0, len(candidateSet))
	for _, u := range candidates {
		if _, ok := candidateSet[u.ID]; ok {
			names = append(names, u.Name)
		}
	}
	return names, nil
}

// loadCrossUsers 加载候选用户列表（参与考勤 + 可选部门/姓名过滤）
func (s *AttendanceRecordService) loadCrossUsers(
	ctx context.Context,
	deptID int64,
	userNames []string,
) ([]model.User, error) {
	var deptIDs []int64
	if deptID > 0 {
		deptIDs = []int64{deptID}
	}
	users, err := s.userRepo.ListAttendanceCandidates(ctx, deptIDs)
	if err != nil {
		return nil, err
	}

	if len(userNames) == 0 {
		return users, nil
	}

	// 按姓名精确过滤
	nameSet := make(map[string]struct{}, len(userNames))
	for _, n := range userNames {
		nameSet[n] = struct{}{}
	}
	filtered := make([]model.User, 0)
	for _, u := range users {
		if _, ok := nameSet[u.Name]; ok {
			filtered = append(filtered, u)
		}
	}
	return filtered, nil
}

// getBusyUserSet 查出在指定时间槽有课的用户 ID 集合
func (s *AttendanceRecordService) getBusyUserSet(
	ctx context.Context,
	userIDs []uint,
	slot agenttool.SlotCondition,
) (map[uint]struct{}, error) {
	courses, err := s.courseRepo.ListByUsersDaySection(ctx, userIDs, slot.DayOfWeek, slot.Section)
	if err != nil {
		return nil, err
	}
	busySet := make(map[uint]struct{})
	for _, c := range courses {
		if slot.Week > 0 && !weekutil.ContainsWeek(c.WeekList, slot.Week) {
			continue
		}
		busySet[c.UserID] = struct{}{}
	}
	return busySet, nil
}

// getAbsentUserSet 查出符合任意缺勤条件（OR）的用户 ID 集合
func (s *AttendanceRecordService) getAbsentUserSet(
	ctx context.Context,
	conditions []agenttool.AbsentCondition,
) (map[uint]struct{}, error) {
	absentSet := make(map[uint]struct{})

	for _, cond := range conditions {
		if cond.Date != "" {
			t, err := time.ParseInLocation("2006-01-02", cond.Date, time.Local)
			if err != nil {
				continue
			}
			record, err := s.attendanceRecordRepo.FindByDateSection(ctx, t, cond.Section)
			if err != nil || record == nil {
				continue
			}
			for _, uid := range parseAttendIDs(record.NotArrivedIDs) {
				absentSet[uid] = struct{}{}
			}
		} else if cond.Week > 0 && s.semesterSrv != nil {
			start, end, err := s.semesterSrv.GetWeekDateRange(ctx, cond.Week)
			if err != nil {
				continue
			}
			records, err := s.attendanceRecordRepo.ListByDateRange(ctx, start, end)
			if err != nil {
				continue
			}
			for _, r := range records {
				if r.Section != cond.Section {
					continue
				}
				for _, uid := range parseAttendIDs(r.NotArrivedIDs) {
					absentSet[uid] = struct{}{}
				}
			}
		}
	}
	return absentSet, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 公共辅助函数
// ─────────────────────────────────────────────────────────────────────────────

// parseAttendIDs 解析 attendance_records 中存储的 JSON 整数数组字符串
func parseAttendIDs(s string) []uint {
	if s == "" {
		return nil
	}
	var ids []uint
	_ = json.Unmarshal([]byte(s), &ids)
	return ids
}

// formatAttendRate 将出勤次数/总次数格式化为百分比字符串
func formatAttendRate(onTime, total int) string {
	if total == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(onTime)/float64(total)*100)
}

// parseRateFloat 将 "85.0%" 解析回 0~1 的 float64（用于 HAVING 比较）
func parseRateFloat(s string) float64 {
	s = strings.TrimSuffix(s, "%")
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f / 100
}
