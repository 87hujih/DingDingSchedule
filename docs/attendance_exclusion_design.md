# 考勤排除日期功能设计文档

## 1. 需求背景

在考勤系统中，存在节假日、调休、临时停课等情况，需要在特定日期不进行考勤统计。本方案通过新建「考勤排除日期表」来实现这一需求。

## 2. 数据模型设计

### 2.1 数据表结构

表名：`attendance_exclusions`

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | BIGINT UNSIGNED | PRIMARY KEY, AUTO_INCREMENT | 主键 |
| tenant_id | BIGINT UNSIGNED | NOT NULL, INDEX | 租户ID（多租户隔离） |
| date | DATE | NOT NULL | 排除日期 |
| type | TINYINT | NOT NULL, DEFAULT 1 | 类型：1=节假日 2=调休放假 3=临时停课 |
| reason | VARCHAR(200) | | 原因说明 |
| created_at | DATETIME | | 创建时间 |
| updated_at | DATETIME | | 更新时间 |
| deleted_at | DATETIME | INDEX | 软删除时间 |

**唯一约束**：`UNIQUE INDEX uniq_tenant_date (tenant_id, date, deleted_at)`

### 2.2 Model 定义

文件：`internal/model/attendance_exclusion.go`

```go
package model

import (
	"time"

	"gorm.io/gorm"
)

// AttendanceExclusionType 排除类型
const (
	ExclusionTypeHoliday   = 1 // 节假日
	ExclusionTypeDayOff    = 2 // 调休放假
	ExclusionTypeSuspended = 3 // 临时停课
)

// AttendanceExclusion 考勤排除日期
type AttendanceExclusion struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	TenantID  uint           `gorm:"not null;uniqueIndex:uniq_tenant_date" json:"tenant_id"`
	Date      time.Time      `gorm:"not null;uniqueIndex:uniq_tenant_date;type:date" json:"date"`
	Type      int            `gorm:"not null;default:1" json:"type"`
	Reason    string         `gorm:"size:200" json:"reason"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"uniqueIndex:uniq_tenant_date" json:"-"`
}

func (*AttendanceExclusion) TableName() string {
	return "attendance_exclusions"
}
```

## 3. Repository 层设计

文件：`internal/repository/attendance_exclusion_repository.go`

### 3.1 接口定义

```go
package repository

import (
	"context"
	"time"

	"schedule_server/internal/model"
)

// AttendanceExclusionRepository 考勤排除日期仓储接口
type AttendanceExclusionRepository interface {
	// ExistsByDate 检查指定日期是否为排除日期
	ExistsByDate(ctx context.Context, date time.Time) (bool, error)

	// FindByDateRange 查询日期范围内的排除记录
	FindByDateRange(ctx context.Context, startDate, endDate time.Time) ([]model.AttendanceExclusion, error)

	// Create 创建排除日期
	Create(ctx context.Context, exclusion *model.AttendanceExclusion) error

	// BatchCreate 批量创建排除日期
	BatchCreate(ctx context.Context, exclusions []model.AttendanceExclusion) error

	// Delete 删除排除日期
	Delete(ctx context.Context, id uint) error

	// FindByID 根据ID查询
	FindByID(ctx context.Context, id uint) (*model.AttendanceExclusion, error)
}
```

### 3.2 实现

```go
type attendanceExclusionRepository struct {
	db *gorm.DB
}

func NewAttendanceExclusionRepository(db *gorm.DB) AttendanceExclusionRepository {
	return &attendanceExclusionRepository{db: db}
}

// ExistsByDate 检查指定日期是否为排除日期（核心方法）
func (r *attendanceExclusionRepository) ExistsByDate(ctx context.Context, date time.Time) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.AttendanceExclusion{}).
		Where("date = ?", date.Format("2006-01-02")).
		Count(&count).Error
	return count > 0, err
}

// FindByDateRange 查询日期范围内的排除记录
func (r *attendanceExclusionRepository) FindByDateRange(ctx context.Context, startDate, endDate time.Time) ([]model.AttendanceExclusion, error) {
	var exclusions []model.AttendanceExclusion
	err := r.db.WithContext(ctx).
		Where("date >= ? AND date <= ?", startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
		Order("date ASC").
		Find(&exclusions).Error
	return exclusions, err
}

// Create 创建排除日期
func (r *attendanceExclusionRepository) Create(ctx context.Context, exclusion *model.AttendanceExclusion) error {
	return r.db.WithContext(ctx).Create(exclusion).Error
}

// BatchCreate 批量创建排除日期（忽略重复）
func (r *attendanceExclusionRepository) BatchCreate(ctx context.Context, exclusions []model.AttendanceExclusion) error {
	if len(exclusions) == 0 {
		return nil
	}
	// 使用 ON DUPLICATE KEY UPDATE 避免重复插入报错
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "date"}, {Name: "deleted_at"}},
			DoUpdates: clause.AssignmentColumns([]string{"type", "reason", "updated_at"}),
		}).
		Create(&exclusions).Error
}

// Delete 删除排除日期
func (r *attendanceExclusionRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.AttendanceExclusion{}, id).Error
}

// FindByID 根据ID查询
func (r *attendanceExclusionRepository) FindByID(ctx context.Context, id uint) (*model.AttendanceExclusion, error) {
	var exclusion model.AttendanceExclusion
	err := r.db.WithContext(ctx).First(&exclusion, id).Error
	if err != nil {
		return nil, err
	}
	return &exclusion, nil
}
```

## 4. Service 层设计

文件：`internal/service/attendance_exclusion_service.go`

```go
package service

import (
	"context"
	"time"

	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"

	"go.uber.org/zap"
)

type AttendanceExclusionService struct {
	repo   repository.AttendanceExclusionRepository
	logger *zap.SugaredLogger
}

func NewAttendanceExclusionService(
	repo repository.AttendanceExclusionRepository,
	logger *zap.SugaredLogger,
) *AttendanceExclusionService {
	return &AttendanceExclusionService{
		repo:   repo,
		logger: logger,
	}
}

// IsExcludedDate 检查日期是否为排除日期（供调度器调用）
func (s *AttendanceExclusionService) IsExcludedDate(ctx context.Context, date time.Time) (bool, error) {
	return s.repo.ExistsByDate(ctx, date)
}

// ListByMonth 按月查询排除日期
func (s *AttendanceExclusionService) ListByMonth(ctx context.Context, year, month int) ([]dto.AttendanceExclusionItem, error) {
	// 计算月份的起止日期
	startDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	endDate := startDate.AddDate(0, 1, -1) // 当月最后一天

	exclusions, err := s.repo.FindByDateRange(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	items := make([]dto.AttendanceExclusionItem, 0, len(exclusions))
	for _, e := range exclusions {
		items = append(items, dto.AttendanceExclusionItem{
			ID:     e.ID,
			Date:   e.Date.Format("2006-01-02"),
			Type:   e.Type,
			Reason: e.Reason,
		})
	}
	return items, nil
}

// BatchAdd 批量添加排除日期
func (s *AttendanceExclusionService) BatchAdd(ctx context.Context, req *dto.BatchAddExclusionRequest) error {
	if len(req.Dates) == 0 {
		return response.ErrInvalidParamWithMsg("日期列表不能为空")
	}

	exclusions := make([]model.AttendanceExclusion, 0, len(req.Dates))
	for _, dateStr := range req.Dates {
		date, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil {
			return response.ErrInvalidParamWithMsg("日期格式错误: " + dateStr)
		}
		exclusions = append(exclusions, model.AttendanceExclusion{
			Date:   date,
			Type:   req.Type,
			Reason: req.Reason,
		})
	}

	return s.repo.BatchCreate(ctx, exclusions)
}

// Delete 删除排除日期
func (s *AttendanceExclusionService) Delete(ctx context.Context, id uint) error {
	_, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return response.ErrNotFound()
	}
	return s.repo.Delete(ctx, id)
}
```

## 5. DTO 设计

文件：`internal/dto/attendance_exclusion.go`

```go
package dto

// AttendanceExclusionItem 排除日期列表项
type AttendanceExclusionItem struct {
	ID     uint   `json:"id"`
	Date   string `json:"date"`
	Type   int    `json:"type"`
	Reason string `json:"reason"`
}

// BatchAddExclusionRequest 批量添加排除日期请求
type BatchAddExclusionRequest struct {
	Dates  []string `json:"dates" binding:"required,min=1"`  // 日期列表，格式：2006-01-02
	Type   int      `json:"type" binding:"required,min=1,max=3"` // 类型
	Reason string   `json:"reason" binding:"max=200"` // 原因
}

// ListExclusionRequest 查询排除日期请求
type ListExclusionRequest struct {
	Year  int `form:"year" binding:"required,min=2020,max=2100"`
	Month int `form:"month" binding:"required,min=1,max=12"`
}

// ListExclusionResponse 查询排除日期响应
type ListExclusionResponse struct {
	Items []AttendanceExclusionItem `json:"items"`
}
```

## 6. Handler 层设计

文件：`internal/handler/attendance_exclusion_handler.go`

```go
package handler

import (
	"strconv"

	"schedule_server/internal/dto"
	"schedule_server/internal/response"
	"schedule_server/internal/service"

	"github.com/gin-gonic/gin"
)

type AttendanceExclusionHandler struct {
	srv *service.AttendanceExclusionService
}

func NewAttendanceExclusionHandler(srv *service.AttendanceExclusionService) *AttendanceExclusionHandler {
	return &AttendanceExclusionHandler{srv: srv}
}

// List 查询排除日期列表
// GET /api/admin/attendance-exclusions?year=2026&month=1
func (h *AttendanceExclusionHandler) List(ctx *gin.Context) {
	var req dto.ListExclusionRequest
	if err := ctx.ShouldBindQuery(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, "参数错误")
		return
	}

	items, err := h.srv.ListByMonth(ctx.Request.Context(), req.Year, req.Month)
	if err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, dto.ListExclusionResponse{Items: items})
}

// BatchAdd 批量添加排除日期
// POST /api/admin/attendance-exclusions
func (h *AttendanceExclusionHandler) BatchAdd(ctx *gin.Context) {
	var req dto.BatchAddExclusionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.Fail(ctx, response.CodeInvalidParam, "参数错误")
		return
	}

	if err := h.srv.BatchAdd(ctx.Request.Context(), &req); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, nil)
}

// Delete 删除排除日期
// DELETE /api/admin/attendance-exclusions/:id
func (h *AttendanceExclusionHandler) Delete(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		response.Fail(ctx, response.CodeInvalidParam, "无效的ID")
		return
	}

	if err := h.srv.Delete(ctx.Request.Context(), uint(id)); err != nil {
		response.FailWithError(ctx, err)
		return
	}

	response.OK(ctx, nil)
}
```

## 7. 路由注册

文件：`internal/app/routers_attendance_exclusion.go`

```go
package app

import (
	"schedule_server/internal/handler"
	"schedule_server/internal/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterAttendanceExclusionRoutes 注册考勤排除日期路由
func RegisterAttendanceExclusionRoutes(r *gin.RouterGroup, h *handler.AttendanceExclusionHandler) {
	// 管理员接口
	adminGroup := r.Group("/admin/attendance-exclusions")
	adminGroup.Use(middleware.AdminOnly()) // 仅管理员可访问
	{
		adminGroup.GET("", h.List)       // 查询列表
		adminGroup.POST("", h.BatchAdd)  // 批量添加
		adminGroup.DELETE("/:id", h.Delete) // 删除
	}
}
```

## 8. 调度器集成（核心改动）

### 8.1 修改 AttendanceScheduler 结构体

文件：`internal/scheduler/attendance_scheduler.go`

```go
// 在结构体中添加排除日期服务
type AttendanceScheduler struct {
	// ... 原有字段 ...
	exclusionSrv *service.AttendanceExclusionService // 新增
}

// 修改构造函数
func NewAttendanceScheduler(
	// ... 原有参数 ...
	exclusionSrv *service.AttendanceExclusionService, // 新增参数
) *AttendanceScheduler {
	return &AttendanceScheduler{
		// ... 原有赋值 ...
		exclusionSrv: exclusionSrv, // 新增
	}
}
```

### 8.2 修改 triggerAttendanceForTenant 方法

```go
// triggerAttendanceForTenant 触发单个租户的考勤统计
func (s *AttendanceScheduler) triggerAttendanceForTenant(tenantID uint, section int, now time.Time) {
	ctx := tenantctx.WithTenantID(context.Background(), tenantID)

	// ========== 新增：检查是否为排除日期 ==========
	excluded, err := s.exclusionSrv.IsExcludedDate(ctx, now)
	if err != nil {
		s.logger.Warnw("检查排除日期失败，继续执行考勤",
			"tenantId", tenantID,
			"date", now.Format("2006-01-02"),
			"err", err,
		)
		// 检查失败时不跳过，继续执行（保守策略）
	}
	if excluded {
		s.logger.Infow("当天为排除日期，跳过考勤统计",
			"tenantId", tenantID,
			"date", now.Format("2006-01-02"),
			"section", section,
		)
		return // 直接返回，不执行考勤
	}
	// ========== 新增结束 ==========

	// ... 原有的考勤逻辑保持不变 ...
}
```

## 9. 依赖注入配置

### 9.1 修改 service/service.go

```go
// 在 Service 聚合结构体中添加
type Service struct {
	// ... 原有字段 ...
	AttendanceExclusion *AttendanceExclusionService
}
```

### 9.2 修改 handler/handler.go

```go
// 在 Handler 聚合结构体中添加
type Handler struct {
	// ... 原有字段 ...
	AttendanceExclusion *AttendanceExclusionHandler
}
```

### 9.3 修改 inits/database.go

在 AutoMigrate 中添加新模型：

```go
if err := db.AutoMigrate(
	// ... 原有模型 ...
	&model.AttendanceExclusion{}, // 新增
); err != nil {
	return nil, err
}
```

## 10. API 接口文档

### 10.1 查询排除日期列表

```
GET /api/admin/attendance-exclusions?year=2026&month=1
Authorization: Bearer <token>

响应:
{
  "code": 0,
  "message": "success",
  "data": {
    "items": [
      {
        "id": 1,
        "date": "2026-01-01",
        "type": 1,
        "reason": "元旦"
      },
      {
        "id": 2,
        "date": "2026-01-26",
        "type": 1,
        "reason": "春节"
      }
    ]
  }
}
```

### 10.2 批量添加排除日期

```
POST /api/admin/attendance-exclusions
Authorization: Bearer <token>
Content-Type: application/json

请求体:
{
  "dates": ["2026-01-26", "2026-01-27", "2026-01-28"],
  "type": 1,
  "reason": "春节假期"
}

响应:
{
  "code": 0,
  "message": "success",
  "data": null
}
```

### 10.3 删除排除日期

```
DELETE /api/admin/attendance-exclusions/1
Authorization: Bearer <token>

响应:
{
  "code": 0,
  "message": "success",
  "data": null
}
```

## 11. 实现步骤清单

按以下顺序实现：

1. [ ] 创建 `internal/model/attendance_exclusion.go`
2. [ ] 修改 `inits/database.go`，添加 AutoMigrate
3. [ ] 创建 `internal/repository/attendance_exclusion_repository.go`
4. [ ] 创建 `internal/dto/attendance_exclusion.go`
5. [ ] 创建 `internal/service/attendance_exclusion_service.go`
6. [ ] 创建 `internal/handler/attendance_exclusion_handler.go`
7. [ ] 创建 `internal/app/routers_attendance_exclusion.go`
8. [ ] 修改 `internal/service/service.go`，添加依赖
9. [ ] 修改 `internal/handler/handler.go`，添加依赖
10. [ ] 修改 `internal/app/router.go`，注册路由
11. [ ] 修改 `internal/scheduler/attendance_scheduler.go`，集成排除检查
12. [ ] 编写单元测试

## 12. 测试用例

### 12.1 Repository 测试

```go
func TestExistsByDate(t *testing.T) {
	// 测试用例:
	// 1. 空表时查询返回 false
	// 2. 插入记录后查询对应日期返回 true
	// 3. 查询不存在的日期返回 false
	// 4. 软删除后查询返回 false
}

func TestBatchCreate(t *testing.T) {
	// 测试用例:
	// 1. 批量插入新记录成功
	// 2. 重复日期自动更新而非报错
	// 3. 空列表不报错
}
```

### 12.2 Scheduler 集成测试

```go
func TestTriggerAttendance_ExcludedDate(t *testing.T) {
	// 测试用例:
	// 1. 普通日期正常触发考勤
	// 2. 排除日期跳过考勤（检查日志输出）
	// 3. 检查失败时继续执行考勤（保守策略）
}
```

## 13. 周期性规则设计（解决周日不考勤问题）

### 13.1 问题场景

| 场景 | 模式 | 规则描述 |
|------|------|----------|
| 假期期间周日不考勤 | holiday | 周日全天所有节次不考勤 |
| 上学期间周日晚上不考勤 | school | 周日仅晚上节次（如第4、5节）不考勤 |

### 13.2 解决方案：新增「周期性规则表」

除了「考勤排除日期表」处理特定日期（如法定节假日），还需要新增「考勤周期规则表」处理每周重复的规则。

#### 数据表结构

表名：`attendance_weekly_rules`

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | BIGINT UNSIGNED | PRIMARY KEY | 主键 |
| tenant_id | BIGINT UNSIGNED | NOT NULL, INDEX | 租户ID |
| mode | VARCHAR(20) | NOT NULL | 生效模式：school / holiday / all |
| day_of_week | TINYINT | NOT NULL | 星期几：1-7（周一到周日） |
| excluded_sections | VARCHAR(50) | | 排除的节次，逗号分隔，如 "4,5" 表示第4、5节；空=全天 |
| reason | VARCHAR(200) | | 规则说明 |
| is_active | TINYINT | NOT NULL, DEFAULT 1 | 是否启用 |
| created_at | DATETIME | | 创建时间 |
| updated_at | DATETIME | | 更新时间 |
| deleted_at | DATETIME | INDEX | 软删除时间 |

**唯一约束**：`UNIQUE INDEX uniq_tenant_mode_day (tenant_id, mode, day_of_week, deleted_at)`

#### Model 定义

文件：`internal/model/attendance_weekly_rule.go`

```go
package model

import (
	"time"

	"gorm.io/gorm"
)

// AttendanceWeeklyRule 考勤周期规则（每周重复）
type AttendanceWeeklyRule struct {
	ID               uint           `gorm:"primaryKey" json:"id"`
	TenantID         uint           `gorm:"not null;uniqueIndex:uniq_tenant_mode_day" json:"tenant_id"`
	Mode             string         `gorm:"size:20;not null;uniqueIndex:uniq_tenant_mode_day" json:"mode"` // school/holiday/all
	DayOfWeek        int            `gorm:"not null;uniqueIndex:uniq_tenant_mode_day" json:"day_of_week"` // 1-7
	ExcludedSections string         `gorm:"size:50" json:"excluded_sections"` // "4,5" 或空（全天）
	Reason           string         `gorm:"size:200" json:"reason"`
	IsActive         bool           `gorm:"not null;default:true" json:"is_active"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"uniqueIndex:uniq_tenant_mode_day" json:"-"`
}

func (*AttendanceWeeklyRule) TableName() string {
	return "attendance_weekly_rules"
}
```

### 13.3 配置示例

#### 示例1：假期期间周日全天不考勤

```json
{
  "mode": "holiday",
  "day_of_week": 7,
  "excluded_sections": "",  // 空表示全天
  "reason": "假期周日休息"
}
```

#### 示例2：上学期间周日晚上（第4、5节）不考勤

```json
{
  "mode": "school",
  "day_of_week": 7,
  "excluded_sections": "4,5",
  "reason": "上学期间周日晚上休息"
}
```

#### 示例3：任何模式下周六全天不考勤

```json
{
  "mode": "all",
  "day_of_week": 6,
  "excluded_sections": "",
  "reason": "周六休息"
}
```

### 13.4 Repository 设计

文件：`internal/repository/attendance_weekly_rule_repository.go`

```go
package repository

import (
	"context"

	"schedule_server/internal/model"
)

type AttendanceWeeklyRuleRepository interface {
	// FindActiveRules 获取指定模式下生效的规则
	// mode: 当前模式（school/holiday）
	// 返回: 匹配 mode 或 "all" 的所有活跃规则
	FindActiveRules(ctx context.Context, mode string) ([]model.AttendanceWeeklyRule, error)

	// IsExcluded 检查指定星期几、节次是否被排除
	IsExcluded(ctx context.Context, mode string, dayOfWeek int, section int) (bool, error)

	// CRUD 方法
	Create(ctx context.Context, rule *model.AttendanceWeeklyRule) error
	Update(ctx context.Context, rule *model.AttendanceWeeklyRule) error
	Delete(ctx context.Context, id uint) error
	List(ctx context.Context) ([]model.AttendanceWeeklyRule, error)
}
```

#### 核心实现

```go
// IsExcluded 检查是否被周期规则排除
func (r *attendanceWeeklyRuleRepository) IsExcluded(
	ctx context.Context,
	mode string,
	dayOfWeek int,
	section int,
) (bool, error) {
	var rules []model.AttendanceWeeklyRule

	// 查询匹配的规则：mode 相同或 mode="all"，且 day_of_week 匹配
	err := r.db.WithContext(ctx).
		Where("is_active = ?", true).
		Where("(mode = ? OR mode = ?)", mode, "all").
		Where("day_of_week = ?", dayOfWeek).
		Find(&rules).Error
	if err != nil {
		return false, err
	}

	for _, rule := range rules {
		// excluded_sections 为空表示全天排除
		if rule.ExcludedSections == "" {
			return true, nil
		}
		// 检查节次是否在排除列表中
		if containsSection(rule.ExcludedSections, section) {
			return true, nil
		}
	}
	return false, nil
}

// containsSection 检查节次是否在逗号分隔的列表中
func containsSection(sections string, section int) bool {
	for _, s := range strings.Split(sections, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if n, err := strconv.Atoi(s); err == nil && n == section {
			return true
		}
	}
	return false
}
```

### 13.5 调度器集成（完整版）

修改 `triggerAttendanceForTenant` 方法，整合「日期排除」和「周期规则」两种检查：

```go
func (s *AttendanceScheduler) triggerAttendanceForTenant(tenantID uint, section int, now time.Time) {
	ctx := tenantctx.WithTenantID(context.Background(), tenantID)
	date := now.Format("2006-01-02")
	dayOfWeek := int(now.Weekday()) // 0=周日，需转换
	if dayOfWeek == 0 {
		dayOfWeek = 7 // 转为 1-7 格式
	}

	// ========== 检查1：特定日期排除（节假日等） ==========
	excluded, err := s.exclusionSrv.IsExcludedDate(ctx, now)
	if err != nil {
		s.logger.Warnw("检查排除日期失败", "tenantId", tenantID, "err", err)
	}
	if excluded {
		s.logger.Infow("当天为排除日期，跳过考勤",
			"tenantId", tenantID, "date", date, "section", section)
		return
	}

	// ========== 检查2：周期规则排除（如周日不考勤） ==========
	// 获取当前模式
	currentMode, err := s.scheduleSettingRepo.GetCurrentMode(ctx)
	if err != nil {
		s.logger.Warnw("获取当前模式失败，默认使用school", "tenantId", tenantID, "err", err)
		currentMode = "school"
	}

	excludedByRule, err := s.weeklyRuleRepo.IsExcluded(ctx, currentMode, dayOfWeek, section)
	if err != nil {
		s.logger.Warnw("检查周期规则失败", "tenantId", tenantID, "err", err)
	}
	if excludedByRule {
		s.logger.Infow("命中周期排除规则，跳过考勤",
			"tenantId", tenantID,
			"date", date,
			"dayOfWeek", dayOfWeek,
			"section", section,
			"mode", currentMode,
		)
		return
	}

	// ========== 通过所有检查，执行考勤 ==========
	// ... 原有考勤逻辑 ...
}
```

### 13.6 API 接口

#### 查询周期规则列表

```
GET /api/admin/attendance-weekly-rules
Authorization: Bearer <token>

响应:
{
  "code": 0,
  "data": {
    "items": [
      {
        "id": 1,
        "mode": "holiday",
        "day_of_week": 7,
        "day_of_week_name": "周日",
        "excluded_sections": "",
        "excluded_sections_desc": "全天",
        "reason": "假期周日休息",
        "is_active": true
      },
      {
        "id": 2,
        "mode": "school",
        "day_of_week": 7,
        "day_of_week_name": "周日",
        "excluded_sections": "4,5",
        "excluded_sections_desc": "第4、5节",
        "reason": "上学期间周日晚上休息",
        "is_active": true
      }
    ]
  }
}
```

#### 创建/更新周期规则

```
POST /api/admin/attendance-weekly-rules
Content-Type: application/json

{
  "mode": "school",
  "day_of_week": 7,
  "excluded_sections": "4,5",
  "reason": "上学期间周日晚上休息"
}
```

### 13.7 完整排除检查流程图

```
┌──────────────────────────────────────────────────────┐
│           调度器触发考勤 (tenantID, section, now)      │
└──────────────────────────────────────────────────────┘
                           │
                           ▼
┌──────────────────────────────────────────────────────┐
│  检查1：日期排除表 (attendance_exclusions)             │
│  - 查询当天是否在排除日期列表中                          │
│  - 如：2026-01-26 春节                                │
└──────────────────────────────────────────────────────┘
                           │
                    是否排除？
                    ├── 是 ──▶ 跳过考勤，记录日志
                    │
                    ▼ 否
┌──────────────────────────────────────────────────────┐
│  检查2：周期规则表 (attendance_weekly_rules)           │
│  - 获取当前模式 (school/holiday)                       │
│  - 查询匹配的规则 (mode + day_of_week)                 │
│  - 检查 section 是否在 excluded_sections 中            │
└──────────────────────────────────────────────────────┘
                           │
                    是否排除？
                    ├── 是 ──▶ 跳过考勤，记录日志
                    │
                    ▼ 否
┌──────────────────────────────────────────────────────┐
│              执行正常考勤统计逻辑                       │
└──────────────────────────────────────────────────────┘
```

### 13.8 两种排除机制对比

| 特性 | 日期排除表 | 周期规则表 |
|------|-----------|------------|
| 适用场景 | 特定日期（节假日、临时停课） | 每周重复规则（周日休息等） |
| 粒度 | 按天 | 按天+节次+模式 |
| 配置方式 | 逐个日期添加 | 配置一次，自动重复生效 |
| 模式感知 | 否 | 是（school/holiday/all） |
| 示例 | 2026-01-26 春节不考勤 | 假期周日不考勤 |

### 13.9 实现步骤补充

在原有12步基础上，新增以下步骤：

13. [ ] 创建 `internal/model/attendance_weekly_rule.go`
14. [ ] 修改 `inits/database.go`，添加 AutoMigrate
15. [ ] 创建 `internal/repository/attendance_weekly_rule_repository.go`
16. [ ] 创建 `internal/dto/attendance_weekly_rule.go`
17. [ ] 创建 `internal/service/attendance_weekly_rule_service.go`
18. [ ] 创建 `internal/handler/attendance_weekly_rule_handler.go`
19. [ ] 创建 `internal/app/routers_attendance_weekly_rule.go`
20. [ ] 修改 `internal/scheduler/attendance_scheduler.go`，集成周期规则检查

---

## 14. 扩展考虑

### 14.1 调休补班场景（可选）

如需支持「调休补班」（如周末补上班），可在日期排除表中扩展：

```go
const (
	ExclusionTypeHoliday   = 1 // 节假日（不考勤）
	ExclusionTypeDayOff    = 2 // 调休放假（不考勤）
	ExclusionTypeSuspended = 3 // 临时停课（不考勤）
	ExclusionTypeWorkday   = 4 // 调休补班（强制考勤，覆盖周期规则）
)
```

调度器逻辑调整为：
- type=4 时，跳过周期规则检查，强制执行考勤
- type=1/2/3 时，跳过考勤

### 14.2 批量导入法定节假日

可提供工具方法，一键导入全年法定节假日：

```go
// ImportChinaHolidays 导入中国法定节假日
func (s *AttendanceExclusionService) ImportChinaHolidays(ctx context.Context, year int) error {
	// 从配置或API获取节假日列表
	// 批量写入数据库
}
```

### 14.3 日历视图支持

前端可实现日历视图，直观展示哪些日期被排除，支持点选添加/删除。