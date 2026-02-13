package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	EnqueuedJobs  prometheus.Counter
	ProcessedJobs prometheus.Counter
	FailedJobs    prometheus.Counter
	UpdatesTotal  prometheus.Counter
}

var (
	once   sync.Once
	global *Metrics
)

func Global() *Metrics {
	once.Do(func() {
		global = &Metrics{
			EnqueuedJobs: prometheus.NewCounter(prometheus.CounterOpts{
				Namespace: "hyprbot",
				Name:      "queue_enqueued_total",
				Help:      "Total jobs enqueued to redis stream",
			}),
			ProcessedJobs: prometheus.NewCounter(prometheus.CounterOpts{
				Namespace: "hyprbot",
				Name:      "queue_processed_total",
				Help:      "Total jobs successfully processed",
			}),
			FailedJobs: prometheus.NewCounter(prometheus.CounterOpts{
				Namespace: "hyprbot",
				Name:      "queue_failed_total",
				Help:      "Total jobs failed during processing",
			}),
			UpdatesTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Namespace: "hyprbot",
				Name:      "telegram_updates_total",
				Help:      "Total telegram updates received",
			}),
		}
		prometheus.MustRegister(global.EnqueuedJobs, global.ProcessedJobs, global.FailedJobs, global.UpdatesTotal)
	})
	return global
}
