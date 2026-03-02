package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus instruments for the HTTP layer and business logic.
var Metrics = newMetrics()

type metrics struct {
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	WSConnectionsGauge   prometheus.Gauge
	WSSubscriptionsGauge prometheus.Gauge
	OrdersCreatedTotal   *prometheus.CounterVec
	TradesExecutedTotal  *prometheus.CounterVec
	MatchingLatency      prometheus.Histogram
	DBWorkerQueueSize    prometheus.Gauge
}

func newMetrics() *metrics {
	return &metrics{
		HTTPRequestsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests by method, path and status code.",
		}, []string{"method", "path", "status"}),

		HTTPRequestDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path"}),

		WSConnectionsGauge: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "ws_connections_active",
			Help: "Number of currently active WebSocket connections.",
		}),

		WSSubscriptionsGauge: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "ws_subscriptions_active",
			Help: "Number of currently active WebSocket subscriptions.",
		}),

		OrdersCreatedTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "orders_created_total",
			Help: "Total number of orders created, labelled by stock and side.",
		}, []string{"stock", "side"}),

		TradesExecutedTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "trades_executed_total",
			Help: "Total number of trades executed, labelled by stock.",
		}, []string{"stock"}),

		MatchingLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "matching_engine_latency_seconds",
			Help:    "Time from order submission to match completion.",
			Buckets: []float64{.0001, .0005, .001, .005, .01, .05, .1, .5},
		}),

		DBWorkerQueueSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "db_worker_queue_size",
			Help: "Current number of pending DB write operations.",
		}),
	}
}

// PrometheusMiddleware records HTTP request counts and latencies.
func PrometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start).Seconds()
		path := r.URL.Path
		method := r.Method
		status := strconv.Itoa(wrapped.status)

		Metrics.HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
		Metrics.HTTPRequestDuration.WithLabelValues(method, path).Observe(duration)
	})
}
