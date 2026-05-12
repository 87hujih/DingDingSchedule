package middleware

import (
	"time"

	"schedule_server/global"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	dbPoolOpen = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "db_connection_pool_open",
			Help: "Number of open DB connections",
		},
		func() float64 {
			sqlDB, err := global.DB.DB()
			if err != nil {
				return 0
			}
			return float64(sqlDB.Stats().OpenConnections)
		},
	)

	dbPoolInUse = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "db_connection_pool_in_use",
			Help: "Number of in-use DB connections",
		},
		func() float64 {
			sqlDB, err := global.DB.DB()
			if err != nil {
				return 0
			}
			return float64(sqlDB.Stats().InUse)
		},
	)

	dbPoolIdle = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "db_connection_pool_idle",
			Help: "Number of idle DB connections",
		},
		func() float64 {
			sqlDB, err := global.DB.DB()
			if err != nil {
				return 0
			}
			return float64(sqlDB.Stats().Idle)
		},
	)
)

func init() {
	prometheus.MustRegister(dbPoolOpen, dbPoolInUse, dbPoolIdle)
}

// StartDBPoolMetricsCollector 启动 DB 连接池指标定期采集。
// GaugeFunc 已自动响应 scrape，此函数仅为保持接口兼容。
func StartDBPoolMetricsCollector(_ time.Duration) {
	select {}
}
