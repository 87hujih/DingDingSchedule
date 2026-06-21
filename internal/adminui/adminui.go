package adminui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"schedule_server/global"
	"schedule_server/internal/adminui/tables"

	ada "github.com/GoAdminGroup/go-admin/adapter/gin"
	"github.com/GoAdminGroup/go-admin/engine"
	goadmincfg "github.com/GoAdminGroup/go-admin/modules/config"
	"github.com/GoAdminGroup/go-admin/modules/language"
	"github.com/GoAdminGroup/go-admin/plugins/admin/modules/table"
	"github.com/GoAdminGroup/go-admin/template"
	"github.com/GoAdminGroup/go-admin/template/chartjs"
	"github.com/GoAdminGroup/themes/adminlte"
	_ "github.com/GoAdminGroup/themes/sword"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	_ "github.com/GoAdminGroup/go-admin/modules/db/drivers/mysql"
)

// Mount 将 GoAdmin 管理后台挂载到现有 gin.Engine 上。
//
// - 仅当 global.AppConfig.GoAdmin.Enable=true 时生效
// - 若 GoAdmin 系统表缺失，会返回 error（请先执行 docs/goadmin_mysql_init.sql）
func Mount(r *gin.Engine, gens ...table.GeneratorList) (*engine.Engine, error) {
	if r == nil {
		return nil, errors.New("adminui: gin engine is nil")
	}
	if !global.AppConfig.GoAdmin.Enable {
		return nil, nil
	}
	if global.DB == nil {
		return nil, errors.New("adminui: global.DB is nil")
	}
	if err := ensureGoAdminBootstrapTables(global.DB); err != nil {
		return nil, err
	}

	// uploads 静态资源
	storePath := strings.TrimSpace(global.AppConfig.GoAdmin.StorePath)
	if storePath == "" {
		storePath = "./uploads"
	}
	storePrefix := strings.TrimSpace(global.AppConfig.GoAdmin.StorePrefix)
	if storePrefix == "" {
		storePrefix = "uploads"
	}
	staticRoute := "/" + strings.TrimPrefix(storePrefix, "/")
	r.Static(staticRoute, storePath)

	// go-admin config
	theme := normalizeTheme(global.AppConfig.GoAdmin.Theme)
	lang := normalizeLanguage(global.AppConfig.GoAdmin.Language)
	urlPrefix := strings.TrimSpace(global.AppConfig.GoAdmin.UrlPrefix)
	if urlPrefix == "" {
		urlPrefix = "admin"
	}

	connMaxLifetime := time.Hour
	if d, err := time.ParseDuration(strings.TrimSpace(global.AppConfig.Database.ConnMaxLifetime)); err == nil && d > 0 {
		connMaxLifetime = d
	}

	goCfg := goadmincfg.Config{
		Env: goadmincfg.EnvLocal,
		Databases: goadmincfg.DatabaseList{
			"default": {
				Driver:          goadmincfg.DriverMysql,
				Dsn:             global.AppConfig.Database.DSN(),
				MaxIdleConns:    global.AppConfig.Database.MaxIdleConns,
				MaxOpenConns:    global.AppConfig.Database.MaxOpenConns,
				ConnMaxLifetime: connMaxLifetime,
			},
		},
		UrlPrefix: urlPrefix,
		Store: goadmincfg.Store{
			Path:   storePath,
			Prefix: storePrefix,
		},
		Language:           lang,
		IndexUrl:           "/",
		Debug:              strings.ToLower(strings.TrimSpace(global.AppConfig.Server.Mode)) == "debug",
		AccessAssetsLogOff: true,
		Theme:              theme,
		Title:              global.AppConfig.App.Name,
	}
	if goCfg.Title == "" {
		goCfg.Title = "schedule-server"
	}
	if theme == "adminlte" {
		goCfg.ColorScheme = adminlte.ColorschemeSkinBlack
	}
	if strings.EqualFold(strings.TrimSpace(global.AppConfig.Env), "prod") {
		goCfg.Env = goadmincfg.EnvProd
	}

	// 必要的组件（adminlte 主题会用到 chart 组件）
	template.AddComp(chartjs.NewChart())

	eng := engine.Default()
	if err := eng.AddConfig(&goCfg).
		AddAdapter(new(ada.Gin)).
		AddGenerators(tables.Generators).
		AddGenerators(gens...).
		Use(r); err != nil {
		return nil, fmt.Errorf("adminui: init goadmin failed: %w", err)
	}

	// GoAdmin 默认不会注册 GET /<prefix>（例如 /admin）路由，但登录成功后会跳转到 IndexUrl="/" => /<prefix>。
	// 这里补一个入口，避免登录后落到 404。
	mountPath := "/" + strings.Trim(urlPrefix, "/")
	if mountPath != "/" {
		r.GET(mountPath, func(c *gin.Context) {
			c.Redirect(302, mountPath+"/info/tenants")
		})
	}

	return eng, nil
}

func normalizeLanguage(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return language.CN
	}
	// go-admin 语言值本身就是 string（例如 "cn"/"en"/"tc"/"jp""）
	switch v {
	case language.CN, language.EN, language.TC, language.JP:
		return v
	default:
		return language.CN
	}
}

func normalizeTheme(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "adminlte":
		return "adminlte"
	case "sword":
		return "sword"
	default:
		return "adminlte"
	}
}

func ensureGoAdminBootstrapTables(db *gorm.DB) error {
	if db == nil {
		return errors.New("adminui: db is nil")
	}
	required := []string{
		"goadmin_users",
		"goadmin_menu",
		"goadmin_permissions",
		"goadmin_roles",
		"goadmin_role_users",
		"goadmin_role_menu",
		"goadmin_role_permissions",
		"goadmin_user_permissions",
		"goadmin_operation_log",
		"goadmin_session",
		"goadmin_site",
	}

	var missing []string
	for _, t := range required {
		if !db.Migrator().HasTable(t) {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("GoAdmin 系统表缺失: %s；请先执行 docs/goadmin_mysql_init.sql（只需一次）", strings.Join(missing, ", "))
	}
	return nil
}
