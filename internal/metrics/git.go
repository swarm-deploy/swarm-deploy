package metrics

import "github.com/prometheus/client_golang/prometheus"

type Git interface {
	subsystem

	RecordGitUpdate(repo, result string)
}

type prometheusGit struct {
	updates *prometheus.CounterVec
}

func newPrometheusGit(namespace string) *prometheusGit {
	return &prometheusGit{
		updates: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "git",
				Name:      "git_updates_total",
				Help:      "Number of git update checks grouped by repo and result.",
			},
			[]string{"repo", "result"},
		),
	}
}

func (g *prometheusGit) RecordGitUpdate(repo, result string) {
	g.updates.WithLabelValues(repo, result).Inc()
}

func (g *prometheusGit) collectors() []prometheus.Collector {
	return []prometheus.Collector{g.updates}
}
