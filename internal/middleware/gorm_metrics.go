package middleware

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
)

var (
	dbQueriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_queries_total",
			Help: "Total number of database queries",
		},
		[]string{"operation"},
	)

	dbQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database query duration in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"operation"},
	)
)

func init() {
	prometheus.MustRegister(dbQueriesTotal, dbQueryDuration)
}

const (
	metricsStartTimeKey = "metrics:start_time"
	metricsOperationKey = "metrics:operation"
)

// RegisterGORMCallbacks 注册 GORM 回调，采集各操作类型的查询指标。
func RegisterGORMCallbacks(db *gorm.DB) {
	db.Callback().Query().Before("gorm:query").Register("metrics:before_query", makeGormBefore("SELECT"))
	db.Callback().Query().After("gorm:query").Register("metrics:after_query", gormAfterQuery)

	db.Callback().Create().Before("gorm:create").Register("metrics:before_create", makeGormBefore("INSERT"))
	db.Callback().Create().After("gorm:create").Register("metrics:after_create", gormAfterQuery)

	db.Callback().Update().Before("gorm:update").Register("metrics:before_update", makeGormBefore("UPDATE"))
	db.Callback().Update().After("gorm:update").Register("metrics:after_update", gormAfterQuery)

	db.Callback().Delete().Before("gorm:delete").Register("metrics:before_delete", makeGormBefore("DELETE"))
	db.Callback().Delete().After("gorm:delete").Register("metrics:after_delete", gormAfterQuery)
}

func makeGormBefore(operation string) func(*gorm.DB) {
	return func(db *gorm.DB) {
		db.Set(metricsStartTimeKey, time.Now())
		db.Set(metricsOperationKey, operation)
	}
}

func gormAfterQuery(db *gorm.DB) {
	val, ok := db.Get(metricsStartTimeKey)
	if !ok {
		return
	}
	start, ok := val.(time.Time)
	if !ok {
		return
	}

	operation := "unknown"
	if op, ok := db.Get(metricsOperationKey); ok {
		operation, _ = op.(string)
	}

	duration := time.Since(start).Seconds()
	dbQueriesTotal.WithLabelValues(operation).Inc()
	dbQueryDuration.WithLabelValues(operation).Observe(duration)
}
