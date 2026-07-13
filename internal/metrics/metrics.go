package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	upstreamHealthy *prometheus.GaugeVec
	Registry        *prometheus.Registry
}

func New() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "Total HTTP requests processed, by route, method, and status.",
		}, []string{"route", "method", "status"}),

		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds, by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),

		upstreamHealthy: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_upstream_healthy",
			Help: "1 if the upstream is currently considered healthy, 0 otherwise.",
		}, []string{"route", "upstream"}),

		Registry: reg,
	}

	reg.MustRegister(m.requestsTotal, m.requestDuration, m.upstreamHealthy)
	return m
}

func (m *Metrics) ObserveRequest(route, method string, status int, duration time.Duration) {
	m.requestsTotal.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	m.requestDuration.WithLabelValues(route).Observe(duration.Seconds())
}

func (m *Metrics) SetUpstreamHealthy(route, upstream string, healthy bool) {
	v := 0.0
	if healthy {
		v = 1
	}
	m.upstreamHealthy.WithLabelValues(route, upstream).Set(v)
}
