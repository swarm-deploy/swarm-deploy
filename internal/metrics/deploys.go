package metrics

import "github.com/prometheus/client_golang/prometheus"

type Deploys interface {
	subsystem

	// RecordInitJobRun records one init job run by stack and service.
	RecordInitJobRun(stack, service string)
	RecordDeploy(stack, service, status string)
}

type prometheusDeploys struct {
	total       *prometheus.CounterVec
	initJobRuns *prometheus.CounterVec
}

func newPrometheusDeploys(namespace string) *prometheusDeploys {
	return &prometheusDeploys{
		total: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "deploys",
				Name:      "total",
				Help:      "Number of deployments grouped by stack, service and status.",
			},
			[]string{"stack", "service", "status"},
		),
		initJobRuns: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "deploys",
				Name:      "init_job_runs_total",
				Help:      "Number of init job runs grouped by stack and service.",
			},
			[]string{"stack", "service"},
		),
	}
}

func (d *prometheusDeploys) RecordInitJobRun(stack, service string) {
	d.initJobRuns.WithLabelValues(stack, service).Inc()
}

func (d *prometheusDeploys) RecordDeploy(stack, service, status string) {
	d.total.WithLabelValues(stack, service, status).Inc()
}

func (d *prometheusDeploys) collectors() []prometheus.Collector {
	return []prometheus.Collector{d.total, d.initJobRuns}
}
