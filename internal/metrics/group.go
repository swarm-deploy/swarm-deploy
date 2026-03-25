package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

type Group struct {
	Deploys   Deploys
	Git       Git
	Sync      Sync
	Events    Events
	Assistant Assistant
	MCP       MCP

	collectors []prometheus.Collector
}

type CreateGroupParams struct {
	Namespace string

	Assistant bool
	MCP       bool
}

type subsystem interface {
	collectors() []prometheus.Collector
}

func NewGroup(params CreateGroupParams) *Group {
	group := &Group{
		collectors: make([]prometheus.Collector, 0),
	}

	group.Deploys = newPrometheusDeploys(params.Namespace)
	group.register(group.Deploys)

	group.Git = newPrometheusGit(params.Namespace)
	group.register(group.Git)

	group.Sync = newPrometheusSync(params.Namespace)
	group.register(group.Sync)

	group.Events = newPrometheusEvents(params.Namespace)
	group.register(group.Events)

	group.Assistant = newAssistant(params.Namespace, params.Assistant)
	group.register(group.Assistant)

	group.MCP = newMCP(params.Namespace, params.MCP)
	group.register(group.MCP)

	return group
}

func (g *Group) register(ss subsystem) {
	g.collectors = append(g.collectors, ss.collectors()...)
}

func (g *Group) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range g.collectors {
		collector.Describe(ch)
	}
}

func (g *Group) Collect(ch chan<- prometheus.Metric) {
	for _, collector := range g.collectors {
		collector.Collect(ch)
	}
}
