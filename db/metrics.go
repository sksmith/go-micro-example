package db

import (
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

var (
	dbLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "smfg_inventory_db_latency",
			Help:       "The latency quantiles for the given database request",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{"func"},
	)

	dbVolume = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "smfg_inventory_db_volume",
			Help: "Number of times a given database request was made",
		},
		[]string{"func"},
	)

	dbErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "smfg_inventory_db_errors",
			Help: "Number of times a given database request failed",
		},
		[]string{"func"},
	)
)

type Metric struct {
	funcName string
	start time.Time
}

func StartMetric(funcName string) *Metric {
	dbVolume.With(prometheus.Labels{"func": funcName}).Inc()
	return &Metric{funcName: funcName, start: time.Now()}
}

func (m *Metric) Complete(err error) {
	if err != nil {
		dbErrors.With(prometheus.Labels{"func": m.funcName}).Inc()
	}
	dbLatency.WithLabelValues(m.funcName).Observe(float64(time.Since(m.start).Milliseconds()))
}

func init() {
	prometheus.MustRegister(dbVolume)
	prometheus.MustRegister(dbLatency)
	prometheus.MustRegister(dbErrors)
}
