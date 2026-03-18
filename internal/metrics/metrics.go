package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Recorder struct {
	deployTotal    *prometheus.CounterVec
	gitUpdates     *prometheus.CounterVec
	syncRuns       *prometheus.CounterVec
	syncDurationMs *prometheus.HistogramVec
}

func New(reg prometheus.Registerer) (*Recorder, error) {
	deployTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "swarm_deploy_total",
			Help: "Number of deployments grouped by stack, service and status.",
		},
		[]string{"stack", "service", "status"},
	)

	gitUpdates := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "swarm_git_updates_total",
			Help: "Number of git update checks grouped by repo and result.",
		},
		[]string{"repo", "result"},
	)

	syncRuns := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "swarm_sync_runs_total",
			Help: "Number of sync runs grouped by reason and result.",
		},
		[]string{"reason", "result"},
	)

	syncDurationMs := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "swarm_sync_duration_milliseconds",
			Help:    "Sync run duration in milliseconds.",
			Buckets: []float64{100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 120000},
		},
		[]string{"reason", "result"},
	)

	if err := reg.Register(deployTotal); err != nil {
		return nil, err
	}
	if err := reg.Register(gitUpdates); err != nil {
		return nil, err
	}
	if err := reg.Register(syncRuns); err != nil {
		return nil, err
	}
	if err := reg.Register(syncDurationMs); err != nil {
		return nil, err
	}

	return &Recorder{
		deployTotal:    deployTotal,
		gitUpdates:     gitUpdates,
		syncRuns:       syncRuns,
		syncDurationMs: syncDurationMs,
	}, nil
}

func (r *Recorder) RecordDeploy(stack, service, status string) {
	r.deployTotal.WithLabelValues(stack, service, status).Inc()
}

func (r *Recorder) RecordGitUpdate(repo, result string) {
	r.gitUpdates.WithLabelValues(repo, result).Inc()
}

func (r *Recorder) RecordSyncRun(reason, result string, duration time.Duration) {
	r.syncRuns.WithLabelValues(reason, result).Inc()
	r.syncDurationMs.WithLabelValues(reason, result).Observe(float64(duration.Milliseconds()))
}
