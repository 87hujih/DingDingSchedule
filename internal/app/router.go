package app

import (
	"schedule_server/global"
	"schedule_server/internal/adminui"
	"schedule_server/internal/handler"
	"schedule_server/internal/middleware"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"

	goadmin "github.com/GoAdminGroup/go-admin/engine"
	"github.com/gin-gonic/gin"
)

var goAdminEng *goadmin.Engine

// setupRouter 初始化路由引擎和依赖注入
func setupRouter() *gin.Engine {
	gin.SetMode(global.AppConfig.Server.Mode)

	// 初始化中间件
	middleware.Init(global.AppConfig.JWT)

	// 依赖注入
	repo := repository.NewRepository(global.DB)
	dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)
	svc := service.NewService(repo, dingMgr, global.AppConfig.JWT, global.AppConfig.Schedule, global.Log)
	h := handler.NewHandler(svc, repo)

	// 注册路由
	r := gin.Default()
	registerRoutes(r, h)

	// GoAdmin 后台（可选）
	if global.AppConfig.GoAdmin.Enable {
		eng, err := adminui.Mount(r)
		if err != nil {
			global.Log.Fatalf("初始化 GoAdmin 失败: %v", err)
		}
		goAdminEng = eng
	}

	return r
}

// registerRoutes 注册所有路由
func registerRoutes(r *gin.Engine, h *handler.Handler) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 公开路由（无需鉴权）
	public := r.Group("/api")
	registerAuthRoutes(public, h)

	// 需要鉴权的路由
	protected := r.Group("/api", middleware.JWTAuth())
	registerUserRoutes(protected, h)            // 用户相关
	registerAdminRoutes(protected, h)           // 管理员相关
	registerScheduleRoutes(protected, h)        // 课程相关
	registerAttendanceRoutes(protected, h)      // 考勤相关
	registerSemesterRoutes(protected, h)        // 学期相关
	registerScheduleSettingRoutes(protected, h) // 作息设置相关
}

// registerSemesterRoutes 学期相关路由
func registerSemesterRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	semesters := rg.Group("/semesters")
	semesters.GET("/current", h.SemesterHdl.GetCurrentSemester)
}

// registerScheduleSettingRoutes 作息设置相关路由
func registerScheduleSettingRoutes(rg *gin.RouterGroup, h *handler.Handler) {
	schedule := rg.Group("/schedule")
	{
		schedule.GET("/info", h.ScheduleSettingHdl.GetScheduleInfo)
		schedule.GET("/current-mode", h.ScheduleSettingHdl.GetCurrentMode)
		schedule.POST("/switch-mode", h.ScheduleSettingHdl.SwitchMode)

		// 考勤开关
		schedule.GET("/attendance/status", h.ScheduleSettingHdl.GetAttendanceStatus)
		schedule.POST("/attendance/toggle", h.ScheduleSettingHdl.ToggleAttendance)
	}
}
