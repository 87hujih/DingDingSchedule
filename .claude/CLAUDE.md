# Schedule Server - Development Rules for AI Assistance

## Project Overview

This is a **multi-tenant scheduling and attendance management system** built with Go, deeply integrated with DingTalk (钉钉) platform. It serves educational/training institutions with course scheduling, attendance tracking, and leave approval features.

## Tech Stack

- **Framework**: Gin v1.11+
- **ORM**: GORM v1.31+ with MySQL
- **Config**: Viper
- **Logging**: Zap + Lumberjack
- **Auth**: JWT (golang-jwt/jwt/v5)
- **Admin UI**: GoAdmin
- **External**: DingTalk Open Platform APIs

## Project Structure

```
internal/
├── app/           # HTTP server, router setup, dependency injection
├── handler/       # HTTP handlers (controllers)
├── service/       # Business logic layer
├── repository/    # Data access layer (interfaces + implementations)
├── model/         # GORM models (database entities)
├── dto/           # Request/Response DTOs
├── middleware/    # HTTP middlewares (JWT, tenant isolation)
├── response/      # Unified response format and error handling
├── consts/        # Constants (roles, status codes)
├── tenantctx/     # Tenant context utilities
└── adminui/       # GoAdmin integration

pkg/               # Reusable packages (can be imported by external projects)
├── dingtalk/      # DingTalk API client wrapper
├── jwt/           # JWT utilities
├── weekutil/      # Week calculation utilities
└── pinyinutil/    # Pinyin conversion
```

## Architecture Patterns

### Layered Architecture

Follow the strict layering: `Handler → Service → Repository → Model`

```go
// Handler: HTTP concerns only
func (h *UserHandler) GetByID(ctx *gin.Context) {
    // 1. Extract and validate request params
    // 2. Call service method
    // 3. Return response using response.OK() or response.Fail()
}

// Service: Business logic
func (s *UserService) GetUserById(ctx context.Context, id uint) (*model.User, error) {
    // Business logic, orchestration, validation
    // Call repository methods
}

// Repository: Data access only
func (r *userRepository) FindByID(ctx context.Context, id uint) (*model.User, error) {
    // GORM operations only
}
```

### Multi-Tenant Isolation

This project uses automatic tenant isolation via GORM plugin. **CRITICAL RULES:**

1. **Always pass `context.Context`** through all layers - it carries tenant_id
2. **Never bypass tenant isolation** unless explicitly needed (use `tenantctx.WithSkipTenantScope(ctx)`)
3. Models with `TenantID` field are automatically filtered/populated

```go
// CORRECT: Pass context to repository
func (s *UserService) GetUser(ctx context.Context, id uint) (*model.User, error) {
    return s.userRepo.FindByID(ctx, id)  // tenant_id auto-filtered
}

// WRONG: Using background context loses tenant scope
func (s *UserService) GetUser(id uint) (*model.User, error) {
    return s.userRepo.FindByID(context.Background(), id)  // NO TENANT FILTERING!
}
```

## Coding Conventions

### Naming

- **Files**: `snake_case.go` (e.g., `user_handler.go`, `attendance_service.go`)
- **Packages**: lowercase, single word when possible (e.g., `handler`, `service`, `dto`)
- **Structs**: `PascalCase` (e.g., `UserService`, `AttendanceHandler`)
- **Interfaces**: No "I" prefix, use descriptive names (e.g., `UserRepository` not `IUserRepository`)
- **Methods**: `PascalCase` for exported, `camelCase` for unexported
- **Constants**: `PascalCase` for exported, `camelCase` for unexported

### Repository Pattern

1. Define interface in repository package
2. Implement with unexported struct
3. Use constructor function returning interface

```go
// Interface definition
type UserRepository interface {
    FindByID(ctx context.Context, id uint) (*model.User, error)
    Create(ctx context.Context, user *model.User) error
}

// Unexported implementation
type userRepository struct {
    db *gorm.DB
}

// Constructor returns interface
func NewUserRepository(db *gorm.DB) UserRepository {
    return &userRepository{db: db}
}
```

### Error Handling

Use `response.BizError` for business errors that should be returned to client:

```go
// In service layer - throw business errors
if user == nil {
    return nil, response.ErrNotFound()
}
if !hasPermission {
    return nil, response.ErrForbidden()
}

// In handler layer - handle errors uniformly
result, err := h.service.DoSomething(ctx, req)
if err != nil {
    response.FailWithError(ctx, err)  // Auto-handles BizError
    return
}
response.OK(ctx, result)
```

Predefined error codes are in `internal/response/`. Use them:
- `response.ErrForbidden()` - 403
- `response.ErrNotFound()` - 404
- `response.ErrInvalidParam()` - 400
- `response.NewBizError(code, message)` - custom

### DTO Pattern

Separate internal models from API contracts:

```go
// DTO with JSON tags for API
type GetUserResponse struct {
    ID     uint   `json:"id"`
    Name   string `json:"name"`
    Avatar string `json:"avatar"`
}

// Constructor to convert model → DTO
func NewGetUserResponse(u *model.User) *GetUserResponse {
    return &GetUserResponse{
        ID:     u.ID,
        Name:   u.Name,
        Avatar: u.Avatar,
    }
}
```

### Handler Pattern

Standard handler structure:

```go
func (h *SomeHandler) HandleAction(ctx *gin.Context) {
    // 1. Extract auth info if needed
    userID, role, ok := getViewerAuth(ctx)
    if !ok {
        return  // Response already sent
    }

    // 2. Parse and validate request
    var req dto.SomeRequest
    if err := ctx.ShouldBindJSON(&req); err != nil {
        response.Fail(ctx, response.CodeInvalidParam, "Invalid request body")
        return
    }

    // 3. Call service
    result, err := h.service.DoAction(ctx.Request.Context(), req)
    if err != nil {
        response.FailWithError(ctx, err)
        return
    }

    // 4. Return success response
    response.OK(ctx, dto.NewSomeResponse(result))
}
```

### Context Propagation

**Always use `ctx.Request.Context()`** when calling service methods from handlers:

```go
// CORRECT
result, err := h.service.DoSomething(ctx.Request.Context(), params)

// WRONG - loses tenant context and request cancellation
result, err := h.service.DoSomething(context.Background(), params)
```

## API Design

### Response Format

All API responses follow this structure:

```json
// Success
{
    "code": 0,
    "message": "success",
    "data": { ... }
}

// Error
{
    "code": 40001,
    "message": "User not found",
    "data": null
}
```

### Pagination

Use consistent pagination pattern:

```go
type ListResponse struct {
    Page     int         `json:"page"`
    PageSize int         `json:"page_size"`
    Total    int         `json:"total"`
    Items    []SomeItem  `json:"items"`
}
```

### Route Organization

Routes are organized by module in `internal/app/routes_*.go`:
- `routes_auth.go` - Authentication routes
- `routes_user.go` - User management routes
- `routes_admin.go` - Admin-only routes

When adding new features, create or extend appropriate route file.

## DingTalk Integration

### Client Management

Use `DingTalkClientManager` to get tenant-specific DingTalk client:

```go
// Get client by tenant ID (from context)
client, err := s.dingMgr.GetClient(ctx)

// Get client by corp ID (for callbacks)
client, err := s.dingMgr.GetClientByCorpID(corpID)
```

### Token Handling

DingTalk client handles token refresh automatically. Never cache tokens manually.

## Common Pitfalls to Avoid

1. **Forgetting context propagation** - Always pass context through layers
2. **Direct model exposure** - Use DTOs for API responses
3. **Business logic in handlers** - Keep handlers thin, logic in services
4. **SQL in service layer** - All database access through repository
5. **Hardcoded tenant_id** - Use context-based tenant isolation
6. **Async goroutines with request context** - Use `context.Background()` for background tasks

```go
// WRONG: Request context may be cancelled
go func() {
    s.doBackgroundWork(c.Request.Context())  // May fail!
}()

// CORRECT: Use background context for async work
go func() {
    s.doBackgroundWork(context.Background())
}()
```

## Adding New Features

### Checklist for new entity/feature:

1. **Model** (`internal/model/xxx.go`)
   - Define GORM model with `TenantID` field if tenant-scoped
   - Add to `AutoMigrate` in `inits/database.go`

2. **Repository** (`internal/repository/xxx_repository.go`)
   - Define interface with CRUD methods
   - Implement with `WithContext(ctx)` for all queries

3. **Service** (`internal/service/xxx_service.go`)
   - Implement business logic
   - Use repository for data access
   - Return `BizError` for client-facing errors

4. **DTO** (`internal/dto/xxx.go`)
   - Define request/response structs
   - Add constructor functions

5. **Handler** (`internal/handler/xxx_handler.go`)
   - Thin layer: parse request, call service, return response

6. **Routes** (`internal/app/routes_xxx.go`)
   - Register routes with appropriate middleware

7. **Wire up** (`internal/handler/handler.go`, `internal/service/service.go`)
   - Add to Handler/Service aggregates
   - Update `internal/app/router.go` for dependency injection

## Testing Guidelines

When writing tests:

- Use table-driven tests
- Mock repository interfaces for service tests
- Use test database for integration tests
- Test tenant isolation explicitly

## Documentation

- API documentation goes in `docs/`
- Use Chinese for user-facing docs, English for technical docs
- Keep `PROJECT_SUMMARY.md` updated for major changes

## Business Rules

### Role-Based Access Control

The system has three role levels defined in `internal/consts/role.go`:

- **RoleUser (0)**: Regular user - can only access own data
- **RoleAdmin (1)**: Administrator - can access tenant-wide data
- **RoleSuperAdmin (2)**: Super admin - full access (reserved for future use)

**Authorization pattern:**

```go
// Use authz_scope.go utilities
scope, err := service.VisibleUserScope(ctx, userRepo, viewerID, viewerRole)
if err != nil {
    return nil, err
}

// Scope determines data visibility:
// - Admin: scope.DeptIDs and scope.OnlyUserIDs are empty (no restrictions)
// - Regular user: scope.OnlyUserIDs = [viewerID] (only own data)

// Pass scope to repository for filtering
users, total, err := userRepo.SearchWithScope(ctx, keyword, scope.DeptIDs, scope.OnlyUserIDs, page, pageSize)
```

**When implementing new features:**
1. Always validate `viewerID != 0` at service layer
2. Use `VisibleUserScope` for data filtering (don't implement custom logic)
3. For admin-only operations, check `viewerRole >= consts.RoleAdmin`

### Attendance Calculation Rules

Attendance follows the formula:

```
Should Arrive = Candidates - Busy Users

Where:
- Candidates: Users with status=1 (participating in attendance), optionally filtered by department
- Busy Users: Users who have courses at the specified time slot (filtered by week and weekday)
```

**Time window calculation:**
1. Based on `config.Schedule.Periods` (defined in `configs/*.yaml`)
2. Each section maps to specific start/end times
3. Use `scheduleutil.CalculateSlotTime()` for consistency

**Leave overlap detection:**
- Uses half-open interval `[start, end)` for time range comparison
- Only considers approved leaves from DingTalk
- Checks overlap with calculated time window, not course duration

### Course Schedule Import Rules

**Import behavior:**
1. Always **replaces** all courses for the specified user (via `courseRepo.ReplaceByUser`)
2. Supported formats: `.xls`, `.xlsx`, `.html` (auto-converted to xlsx)
3. Automatically associates with active semester (if exists)
4. Empty imports are rejected

**Week list format:**
- Stored as comma-separated string (e.g., "1,2,3,5,8-10")
- Parsed by `pkg/weekutil` functions
- Week numbers are 1-based

**When implementing course-related features:**
- Never partially update courses - always use replace strategy for data integrity
- Validate week numbers against semester configuration
- DayOfWeek is 1-7 (Monday to Sunday)
- Section is 1-based (1 = period 1-2, 2 = period 3-4, etc.)

### Semester Management

**Active semester concept:**
- Only one semester can be active at a time per tenant
- Active semester is used for:
  - Default association when importing courses
  - Week number validation
  - Attendance record association

**Week number validation:**
- Week numbers must be within `[1, semester.TotalWeeks]`
- Current week is calculated from `semester.StartDate` using `weekutil.GetCurrentWeek()`

### Multi-Tenant Data Isolation

**Critical business rule:** All tenant-scoped operations MUST respect tenant boundaries.

**Models with tenant isolation:**
- User, Department, Course, Semester, AttendanceRecord, LeaveApproval, Tenant (itself)
- All have `TenantID` field and are auto-filtered by GORM plugin

**When to skip tenant scope:**
```go
// ONLY for system-level operations like migrations or background tasks
ctx = tenantctx.WithSkipTenantScope(ctx)

// Example: Creating initial tenant record
tenant := &model.Tenant{CorpID: corpID}
db.WithContext(ctx).Create(tenant)  // ctx must skip tenant scope
```

**Never:**
- Hardcode tenant_id in queries
- Share data across tenants (e.g., department references)
- Use tenant_id from user input directly (always use context)

### DingTalk Integration Rules

**Client management:**
- Each tenant has isolated DingTalk credentials (stored in `tenants` table)
- Access tokens are cached per-tenant with auto-refresh (5min before expiry)
- Never create DingTalk client directly - always use `DingTalkClientManager`

**Callback handling:**
- Callbacks must be idempotent (use unique constraints)
- Async processing via goroutines (use `context.Background()`, not request context)
- Always verify signature and decrypt payload
- Map `corp_id` to tenant before processing

**User synchronization:**
- DingTalk user ID (`ding_user_id`) is unique per tenant
- Always upsert (create or update) to handle re-syncs
- Generate pinyin index automatically on sync
- Preserve local user data (role, status) when syncing

### Data Consistency Rules

**Soft deletes:**
- All main entities use GORM soft delete (`DeletedAt` field)
- Queries automatically exclude soft-deleted records
- Use `Unscoped()` only for permanent deletion or admin views

**Transactions:**
- Use for operations affecting multiple tables:
  - User department sync (delete + batch insert)
  - Course replacement (delete + batch insert)
  - Leave approval callback (create approval + update attendance)

**Unique constraints:**
- `users`: `(tenant_id, ding_user_id)` - prevents duplicate DingTalk users
- `departments`: `(tenant_id, dept_id)` - dept_id is from DingTalk
- `leave_approvals`: `(tenant_id, process_instance_id)` - prevents duplicate callbacks
- `tenants`: `corp_id` - unique per DingTalk organization

### API Input Validation

**Validation strategy:**
1. Use struct tags for basic validation: `binding:"required"`, `validate:"min=1,max=100"`
2. Custom validation in service layer for business rules
3. Return `response.ErrInvalidParam()` for validation failures

**Common validations:**
- User ID: must be > 0
- Week number: must be within semester range
- Section: must match configured periods
- Department IDs: must exist in tenant's departments
- Date format: "2006-01-02" (ISO format)

**Example:**
```go
type ImportScheduleRequest struct {
    File *multipart.FileHeader `form:"file" binding:"required"`
}

// In service layer:
if !strings.HasSuffix(file.Filename, ".xlsx") && !strings.HasSuffix(file.Filename, ".xls") {
    return response.ErrInvalidParamWithMsg("Only .xlsx/.xls files are supported")
}
```

### Error Handling Strategy

**Error levels:**

1. **Client errors (4xx)** - return `BizError`:
   - Invalid input: `response.ErrInvalidParam()`
   - Unauthorized: `response.ErrForbidden()`
   - Not found: `response.ErrNotFound()`

2. **External service errors** - wrap and return `BizError`:
   ```go
   client, err := s.dingMgr.GetClient(ctx)
   if err != nil {
       return response.NewBizError(response.CodeExternalAPIError, "Failed to connect to DingTalk")
   }
   ```

3. **Internal errors (5xx)** - log and return generic error:
   ```go
   if err := s.repo.Save(ctx, entity); err != nil {
       s.logger.Errorw("Failed to save entity", "error", err)
       return response.NewBizError(response.CodeInternalError, "Internal server error")
   }
   ```

**Never:**
- Expose internal error details (DB errors, stack traces) to client
- Use panic for business logic errors
- Return `nil` error when operation actually failed
