package metrics

import "github.com/prometheus/client_golang/prometheus"

type Deploys interface {
	subsystem

	RecordDeploy(stack, service, status string)
}

type prometheusDeploys struct {
	total *prometheus.CounterVec
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
	}
}

func (d *prometheusDeploys) RecordDeploy(stack, service, status string) {
	d.total.WithLabelValues(stack, service, status).Inc()
}

func (d *prometheusDeploys) collectors() []prometheus.Collector {
	return []prometheus.Collector{d.total}
}
