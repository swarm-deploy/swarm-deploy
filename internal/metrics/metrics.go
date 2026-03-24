package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Recorder struct {
	deployTotal  *prometheus.CounterVec
	gitUpdates   *prometheus.CounterVec
	syncRuns     *prometheus.CounterVec
	syncDuration *prometheus.HistogramVec
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

	syncDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "swarm_sync_duration_seconds",
			Help:    "Sync run duration in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
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
	if err := reg.Register(syncDuration); err != nil {
		return nil, err
	}
	return &Recorder{
		deployTotal:  deployTotal,
		gitUpdates:   gitUpdates,
		syncRuns:     syncRuns,
		syncDuration: syncDuration,
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
	r.syncDuration.WithLabelValues(reason, result).Observe(duration.Seconds())
}
