package config

import (
	"fmt"
	"time"
)

// Config 总配置
type Config struct {
	Server   Server   `mapstructure:"server" yaml:"server"`
	Database Database `mapstructure:"database" yaml:"database"`
	DingTalk DingTalk `mapstructure:"dingtalk" yaml:"dingtalk"`
	Log      Log      `mapstructure:"log" yaml:"log"`
	JWT      JWT      `mapstructure:"jwt" yaml:"jwt"`
	WiFi     WiFi     `mapstructure:"wifi" yaml:"wifi"`
	Schedule Schedule `mapstructure:"schedule" yaml:"schedule"`
	GoAdmin  GoAdmin  `mapstructure:"goadmin" yaml:"goadmin"`
	Env      string   `mapstructure:"env" yaml:"env"`
	App      App      `mapstructure:"app" yaml:"app"`
}

// Server 配置
type Server struct {
	Port         int    `mapstructure:"port" yaml:"port"`
	Mode         string `mapstructure:"mode" yaml:"mode"`
	ReadTimeout  string `mapstructure:"read_timeout" yaml:"read_timeout"`
	WriteTimeout string `mapstructure:"write_timeout" yaml:"write_timeout"`
}

type App struct {
	Name string `mapstructure:"name" yaml:"name"`
}

// GoAdmin GoAdmin 管理后台配置
type GoAdmin struct {
	Enable bool `mapstructure:"enable" yaml:"enable"`

	// UrlPrefix 管理后台挂载路径前缀（例如 "admin" 或 "/admin"）
	UrlPrefix string `mapstructure:"url_prefix" yaml:"url_prefix"`

	// Theme UI 主题（adminlte / sword）
	Theme string `mapstructure:"theme" yaml:"theme"`

	// Language 界面语言（cn / en / tc / jp / pt-BR）
	Language string `mapstructure:"language" yaml:"language"`

	// StorePath 上传目录（本地存储）
	StorePath string `mapstructure:"store_path" yaml:"store_path"`
	// StorePrefix 上传访问前缀（通常为 "uploads"，对应 gin.Static("/uploads", "./uploads")）
	StorePrefix string `mapstructure:"store_prefix" yaml:"store_prefix"`
}

// Database 配置(MySQL)
type Database struct {
	Host            string `mapstructure:"host" yaml:"host"`
	Port            int    `mapstructure:"port" yaml:"port"`
	User            string `mapstructure:"user" yaml:"user"`
	Password        string `mapstructure:"password" yaml:"password"`
	DBName          string `mapstructure:"dbname" yaml:"dbname"`
	Charset         string `mapstructure:"charset" yaml:"charset"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns" yaml:"max_idle_conns"`
	MaxOpenConns    int    `mapstructure:"max_open_conns" yaml:"max_open_conns"`
	ConnMaxLifetime string `mapstructure:"conn_max_lifetime" yaml:"conn_max_lifetime"`
}

// DSN 返回 GORM 需要的数据库连接字符串
func (d *Database) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=True&loc=Local",
		d.User, d.Password, d.Host, d.Port, d.DBName, d.Charset)
}

// DingTalk 钉钉配置
type DingTalk struct {
	AppKey    string `mapstructure:"app_key" yaml:"app_key"`
	AppSecret string `mapstructure:"app_secret" yaml:"app_secret"`
	AgentID   string `mapstructure:"agent_id" yaml:"agent_id"` // 建议用 string 防止数字过大溢出或丢失前导零
	CorpID    string `mapstructure:"corp_id" yaml:"corp_id"`

	// CallbackToken / CallbackAESKey 用于"事件订阅回调"的验签与解密。
	// 多租户场景下建议所有租户的应用回调配置使用同一套 token/aesKey（否则无法在解密前定位租户）。
	CallbackToken  string `mapstructure:"callback_token" yaml:"callback_token"`
	CallbackAESKey string `mapstructure:"callback_aes_key" yaml:"callback_aes_key"` // 43位 EncodingAESKey（不含末尾 '='）

	// StreamMode 是否启用 Stream 模式接收事件（无需公网 IP）
	StreamMode bool `mapstructure:"stream_mode" yaml:"stream_mode"`
}

// Log 日志配置 (Zap)
type Log struct {
	Level         string `mapstructure:"level" yaml:"level"`                     // debug, info, warn, error
	Encoding      string `mapstructure:"encoding" yaml:"encoding"`               // json 或 console
	Filename      string `mapstructure:"filename" yaml:"filename"`               // 日志文件路径
	MaxSize       int    `mapstructure:"max_size" yaml:"max_size"`               // 单个日志最大体积(MB)
	MaxAge        int    `mapstructure:"max_age" yaml:"max_age"`                 // 保留天数
	MaxBackups    int    `mapstructure:"max_backups" yaml:"max_backups"`         // 最大备份数量
	Compress      bool   `mapstructure:"compress" yaml:"compress"`               // 是否压缩归档
	RotateOnStart bool   `mapstructure:"rotate_on_start" yaml:"rotate_on_start"` // 是否在启动时切割日志
	TimeFormat    string `mapstructure:"time_format" yaml:"time_format"`         // 时间格式（可选，不填则使用默认 ISO8601）
}

// JWT 鉴权配置
type JWT struct {
	Secret string `mapstructure:"secret" yaml:"secret"`
	Expire string `mapstructure:"expire" yaml:"expire"` // 例如 "72h"
	Issuer string `mapstructure:"issuer" yaml:"issuer"`
}

// WiFi 考勤配置
type WiFi struct {
	AllowedSSIDs []string `mapstructure:"allowed_ssids" yaml:"allowed_ssids"`
	AllowedMACs  []string `mapstructure:"allowed_macs" yaml:"allowed_macs"`
}

// Schedule 作息表配置
type Schedule struct {
	Periods []Period `mapstructure:"periods" yaml:"periods"`
}
type Period struct {
	Name  string `mapstructure:"name" yaml:"name"`
	Start string `mapstructure:"start" yaml:"start"` // "08:00"
	End   string `mapstructure:"end" yaml:"end"`     // "09:40"
}

// GetTodayStartTime 获取【今天】该节课的开始时间
func (p *Period) GetTodayStartTime() time.Time {
	return parseTimeOnToday(p.Start)
}

// GetTodayEndTime 获取【今天】该节课的结束时间
func (p *Period) GetTodayEndTime() time.Time {
	return parseTimeOnToday(p.End)
}

// 内部辅助函数：将 "15:04" 转换为今天的具体时间
func parseTimeOnToday(timeStr string) time.Time {
	now := time.Now()
	parsed, _ := time.Parse("15:04", timeStr)
	return time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, now.Location())
}
