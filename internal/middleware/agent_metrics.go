package middleware

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	agentCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_calls_total",
			Help: "Total number of agent calls",
		},
		[]string{"route", "status"},
	)

	agentCallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agent_call_duration_seconds",
			Help:    "Agent call total duration in seconds",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60},
		},
		[]string{"route"},
	)

	agentLLMDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agent_llm_duration_seconds",
			Help:    "LLM call duration in seconds",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30},
		},
		nil,
	)
)

func init() {
	prometheus.MustRegister(agentCallsTotal, agentCallDuration, agentLLMDuration)
}

// ObserveAgentCall 将一次 Agent 调用的指标写入 Prometheus。
// route: 路由类型（如 tool_query / rag / mixed），status: success / failed / timeout。
// durationMs: 调用总耗时（毫秒），llmDurationMs: LLM 耗时（毫秒）。
func ObserveAgentCall(route, status string, durationMs, llmDurationMs int64) {
	if route == "" {
		route = "unknown"
	}
	agentCallsTotal.WithLabelValues(route, status).Inc()
	agentCallDuration.WithLabelValues(route).Observe(float64(durationMs) / 1000)
	if llmDurationMs > 0 {
		agentLLMDuration.WithLabelValues().Observe(float64(llmDurationMs) / 1000)
	}
}
